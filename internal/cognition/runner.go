package cognition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/runtime"
)

type SessionConfigResolver func(context.Context, string, epistemic.Phase) (runtime.SessionConfig, error)

type PhaseRequest struct {
	WorkID     string
	Goal       string
	Phase      epistemic.Phase
	ResourceID string
	Projection epistemic.Projection
	// RunID correlates every event of this invocation. The runner generates
	// one when it is empty.
	RunID string
}

type PhaseResult struct {
	Artifact   PhaseArtifact
	Raw        []byte
	ResourceID string
	SessionID  string
	TurnID     string
	Runtime    string
	Model      string
}

type PhaseRunner struct {
	runtime  runtime.Runtime
	resolve  SessionConfigResolver
	observer PhaseObserver
	stall    time.Duration
}

func NewPhaseRunner(rt runtime.Runtime, resolve SessionConfigResolver) *PhaseRunner {
	return &PhaseRunner{runtime: rt, resolve: resolve}
}

// WithObserver routes every phase event to observe. Fresh phase sessions are
// otherwise invisible: their events never reach the persistent agent views.
func (r *PhaseRunner) WithObserver(observe PhaseObserver) *PhaseRunner {
	r.observer = observe
	return r
}

// WithStallTimeout fails a run whose session produces no activity for d. Zero
// disables stall detection; the caller's context still bounds the run.
func (r *PhaseRunner) WithStallTimeout(d time.Duration) *PhaseRunner {
	r.stall = d
	return r
}

func (r *PhaseRunner) Run(ctx context.Context, request PhaseRequest) (result PhaseResult, err error) {
	if r == nil || r.runtime == nil || r.resolve == nil {
		return PhaseResult{}, errors.New("phase runner is not configured")
	}
	obs := newRunObservation(r.observer, &request)
	defer func() { obs.finish(ctx, err) }()
	if request.Phase == "" || request.Projection.Phase != request.Phase {
		return PhaseResult{}, errors.New("phase request and projection do not match")
	}
	config, err := r.resolve(ctx, request.ResourceID, request.Phase)
	if err != nil {
		return PhaseResult{}, fmt.Errorf("resolve phase resource: %w", err)
	}
	if config.AgentID == "" {
		return PhaseResult{}, errors.New("phase resource has no runtime agent")
	}
	obs.agentID = config.AgentID
	phaseInstructions, err := phaseInstruction(request.Phase)
	if err != nil {
		return PhaseResult{}, err
	}
	if config.Instructions != "" {
		config.Instructions += "\n\n"
	}
	config.Instructions += phaseInstructions
	obs.advance(StageSessionStarting)
	obs.emit(PhaseSessionStarting, map[string]string{
		"runtime":            r.runtime.Name(),
		"model":              config.Model,
		"working_dir":        config.WorkingDir,
		"instructions_bytes": strconv.Itoa(len(config.Instructions)),
	})
	session, err := r.runtime.Start(ctx, config)
	if err != nil {
		obs.emit(PhaseSessionFailed, map[string]string{"error": err.Error()})
		return PhaseResult{}, fmt.Errorf("start %s session: %w", request.Phase, err)
	}
	defer session.Close()
	obs.sessionID = session.ID()
	obs.advance(StageSessionStarted)
	obs.emit(PhaseSessionStarted, map[string]string{"runtime": r.runtime.Name(), "model": session.Metadata().Model})
	prompt, err := json.Marshal(struct {
		WorkID          string                    `json:"work_id"`
		Goal            string                    `json:"goal"`
		Phase           epistemic.Phase           `json:"phase"`
		Projection      epistemic.State           `json:"projection"`
		ProjectionIndex map[string][]epistemic.ID `json:"projection_index"`
	}{request.WorkID, request.Goal, request.Phase, request.Projection.State, projectionIndex(request.Projection.State)})
	if err != nil {
		return PhaseResult{}, err
	}
	outputSchema := PhaseOutputSchema(request.Phase, request.Projection)
	if err := session.Send(ctx, runtime.Input{Text: string(prompt), OutputSchema: outputSchema}); err != nil {
		obs.emit(PhaseTurnFailed, map[string]string{"error": err.Error(), "during": "send"})
		return PhaseResult{}, fmt.Errorf("send %s request: %w", request.Phase, err)
	}
	obs.advance(StageTurnRequested)
	obs.emit(PhaseTurnRequested, map[string]string{"prompt_bytes": strconv.Itoa(len(prompt)), "prompt_digest": Digest(prompt)})

	var stall <-chan time.Time
	if r.stall > 0 {
		ticker := time.NewTicker(stallPoll(r.stall))
		defer ticker.Stop()
		stall = ticker.C
	}
	// A turn may deliver its answer as deltas, as one completed message, or
	// both. The completed message is authoritative; the deltas are its stream.
	var output strings.Builder
	finalMessage := ""
	turnID := ""
	repairAttempts := 0
	const maxArtifactRepairs = 2
	for {
		select {
		case <-ctx.Done():
			return PhaseResult{}, ctx.Err()
		case <-stall:
			// An open command or tool has its own lifecycle. A
			// quiet interval while it is in progress is not
			// evidence that the phase is stalled: the runtime may
			// simply have no intermediate output to report. The
			// enclosing phase context remains the safety bound.
			if quiet := obs.sinceActivity(); quiet >= r.stall && len(obs.openItems) == 0 {
				obs.fail(PhaseStalled, map[string]string{"stall_after_ms": strconv.FormatInt(r.stall.Milliseconds(), 10)})
				return PhaseResult{}, fmt.Errorf("%w: no activity for %s", ErrPhaseStalled, quiet.Round(time.Second))
			}
		case event, ok := <-session.Events():
			if !ok {
				obs.fail(PhaseTurnFailed, map[string]string{"error": "session event stream closed", "during": "await"})
				return PhaseResult{}, errors.New("phase session ended before turn completion")
			}
			if event.SessionID != "" && event.SessionID != session.ID() {
				obs.ignored(event, "session_mismatch", session.ID())
				continue
			}
			if event.TurnID != "" {
				if turnID == "" {
					turnID = event.TurnID
					obs.turnID = turnID
					obs.advance(StageTurnStarted)
					obs.emit(PhaseTurnStarted, nil)
				}
				if event.TurnID != turnID {
					obs.ignored(event, "turn_mismatch", turnID)
					continue
				}
			}
			method := event.Metadata["backend_method"]
			obs.activity(event, method)
			if event.Kind == runtime.KindMessage && event.Summary != "" {
				switch {
				case method == "" || strings.Contains(method, "delta"):
					output.WriteString(event.Summary)
				case isCompletedMessage(method):
					finalMessage = event.Summary
				}
				continue
			}
			if event.Kind == runtime.KindError {
				obs.fail(PhaseTurnFailed, map[string]string{"error": event.Summary, "backend_method": method, "output_bytes": strconv.Itoa(output.Len())})
				return PhaseResult{}, fmt.Errorf("phase runtime failed: %s", event.Summary)
			}
			if event.Kind == runtime.KindStatus {
				switch method {
				case "turn/failed":
					obs.fail(PhaseTurnFailed, map[string]string{"error": event.Summary, "backend_method": method, "output_bytes": strconv.Itoa(output.Len())})
					return PhaseResult{}, fmt.Errorf("%s turn failed: %s", request.Phase, event.Summary)
				case "turn/completed":
					obs.advance(StageTurnCompleted)
					obs.emit(PhaseTurnCompleted, map[string]string{"output_bytes": strconv.Itoa(output.Len())})
					source, text := ArtifactSourceStreamedDeltas, output.String()
					if strings.TrimSpace(finalMessage) != "" {
						source, text = ArtifactSourceFinalMessage, finalMessage
					}
					raw := []byte(strings.TrimSpace(text))
					obs.emit(PhaseArtifactReceived, map[string]string{"source": source, "bytes": strconv.Itoa(len(raw)), "digest": Digest(raw), "preview": preview(raw)})
					artifact, artifactErr := ParsePhaseArtifact(request.Phase, raw)
					if artifactErr == nil {
						artifactErr = artifact.Validate(request.Projection)
					}
					if artifactErr != nil {
						repairAttempts++
						obs.emit(PhaseArtifactInvalid, map[string]string{
							"error": artifactErr.Error(), "source": source, "bytes": strconv.Itoa(len(raw)),
							"digest": Digest(raw), "preview": preview(raw), "repair_attempt": strconv.Itoa(repairAttempts),
						})
						if repairAttempts > maxArtifactRepairs {
							return PhaseResult{}, fmt.Errorf("artifact repair exhausted after %d attempts: %w", maxArtifactRepairs, artifactErr)
						}
						repairPrompt := fmt.Sprintf(
							"Your phase artifact failed CES validation: %s\nReturn the complete corrected artifact. Do not return a patch or explanation.",
							artifactErr,
						)
						output.Reset()
						finalMessage = ""
						turnID = ""
						obs.turnID = ""
						obs.advance(StageTurnRequested)
						if err := session.Send(ctx, runtime.Input{Text: repairPrompt, OutputSchema: outputSchema}); err != nil {
							obs.fail(PhaseTurnFailed, map[string]string{"error": err.Error(), "during": "repair_send", "repair_attempt": strconv.Itoa(repairAttempts)})
							return PhaseResult{}, fmt.Errorf("send %s artifact repair: %w", request.Phase, err)
						}
						obs.emit(PhaseTurnRequested, map[string]string{
							"prompt_bytes": strconv.Itoa(len(repairPrompt)), "prompt_digest": Digest([]byte(repairPrompt)),
							"repair_attempt": strconv.Itoa(repairAttempts),
						})
						continue
					}
					obs.advance(StageArtifactParsed)
					obs.emit(PhaseArtifactParsed, map[string]string{"digest": Digest(raw)})
					metadata := session.Metadata()
					return PhaseResult{Artifact: artifact, Raw: raw, ResourceID: request.ResourceID, SessionID: session.ID(), TurnID: turnID, Runtime: r.runtime.Name(), Model: metadata.Model}, nil
				}
			}
		}
	}
}

// Where a phase artifact was read from, as recorded on artifact_received.
const (
	ArtifactSourceFinalMessage   = "final_message"
	ArtifactSourceStreamedDeltas = "streamed_deltas"
)

// isCompletedMessage reports a backend event carrying a whole agent message,
// such as Codex's item/completed. Started items carry no text and are ignored.
func isCompletedMessage(method string) bool {
	return strings.Contains(strings.ToLower(method), "completed")
}

// stallPoll checks often enough to report a stall close to its deadline
// without spinning on short intervals.
func stallPoll(d time.Duration) time.Duration {
	p := d / 10
	if p < 10*time.Millisecond {
		return 10 * time.Millisecond
	}
	if p > 30*time.Second {
		return 30 * time.Second
	}
	return p
}

// preview is a short, single-line excerpt of a payload for the trace. The
// payload itself is identified by digest.
func preview(b []byte) string {
	const limit = 160
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

func phaseInstruction(phase epistemic.Phase) (string, error) {
	contract, ok := Contract(phase)
	if !ok {
		return "", fmt.Errorf("no phase contract for %q", phase)
	}
	return contract.String(), nil
}

func projectionIndex(state epistemic.State) map[string][]epistemic.ID {
	index := map[string][]epistemic.ID{}
	add := func(kind epistemic.ObjectKind, id epistemic.ID) {
		if id != "" {
			index[string(kind)] = append(index[string(kind)], id)
		}
	}
	for _, value := range state.Observations {
		add(epistemic.ObjectObservation, value.ID)
	}
	for _, value := range state.Claims {
		add(epistemic.ObjectClaim, value.ID)
	}
	for _, value := range state.Hypotheses {
		add(epistemic.ObjectHypothesis, value.ID)
	}
	for _, value := range state.Unknowns {
		add(epistemic.ObjectUnknown, value.ID)
	}
	for _, value := range state.Constraints {
		add(epistemic.ObjectConstraint, value.ID)
	}
	for _, value := range state.Frames {
		add(epistemic.ObjectFrame, value.ID)
	}
	for _, value := range state.Actions {
		add(epistemic.ObjectAction, value.ID)
	}
	for _, value := range state.Outcomes {
		add(epistemic.ObjectOutcome, value.ID)
	}
	for kind := range index {
		sort.Slice(index[kind], func(i, j int) bool { return index[kind][i] < index[kind][j] })
	}
	return index
}
