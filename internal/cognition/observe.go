package cognition

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/runtime"
)

// Phase lifecycle event names. They are the trace vocabulary for one phase
// run: every name here is written to the JSONL trace, so they are stable.
const (
	PhaseScheduled               = "phase_scheduled"
	PhaseResourceSelected        = "phase_resource_selected"
	PhaseSessionStarting         = "phase_session_starting"
	PhaseSessionStarted          = "phase_session_started"
	PhaseSessionFailed           = "phase_session_failed"
	PhaseTurnRequested           = "phase_turn_requested"
	PhaseTurnStarted             = "phase_turn_started"
	PhaseActivity                = "phase_activity"
	PhaseEventIgnored            = "phase_event_ignored"
	PhaseTurnCompleted           = "phase_turn_completed"
	PhaseTurnFailed              = "phase_turn_failed"
	PhaseArtifactReceived        = "phase_artifact_received"
	PhaseArtifactParsed          = "phase_artifact_parsed"
	PhaseArtifactInvalid         = "phase_artifact_invalid"
	PhaseArtifactRepairAttempt   = "phase_artifact_repair_attempt"
	PhaseArtifactRepairSucceeded = "phase_artifact_repair_succeeded"
	PhaseArtifactRepairExhausted = "phase_artifact_repair_exhausted"
	PhaseDeltaGenerated          = "phase_delta_generated"
	PhaseDeltaFailed             = "phase_delta_failed"
	PhaseCommitSucceeded         = "phase_commit_succeeded"
	PhaseCommitSkipped           = "phase_commit_skipped"
	PhaseCommitFailed            = "phase_commit_failed"
	PhaseAdvanceFailed           = "phase_advance_failed"
	PhaseTransition              = "phase_transition"
	PhaseQACRequest              = "qac_request"
	PhaseQACAllocFailed          = "qac_allocation_failed"
	PhaseNextStarted             = "next_phase_started"
	PhaseFinished                = "phase_finished"
	PhaseTimeout                 = "phase_timeout"
	PhaseStalled                 = "phase_stalled"
	PhaseCanceled                = "phase_canceled"
)

// Stages a phase run passes through. On failure the last reached stage says
// how far the run got, which is what separates "never started" from "finished
// but the terminal event was lost".
const (
	StageResolving       = "resolving"
	StageSessionStarting = "session_starting"
	StageSessionStarted  = "session_started"
	StageTurnRequested   = "turn_requested"
	StageTurnStarted     = "turn_started"
	StageActive          = "active"
	StageTurnCompleted   = "turn_completed"
	StageArtifactParsed  = "artifact_parsed"
)

// ErrPhaseStalled reports a phase session that went quiet for longer than the
// configured stall interval. It is distinct from the wall-clock timeout.
var ErrPhaseStalled = errors.New("phase session stalled")

// PhaseEvent is one observable step of a phase run. Fields carries event
// specific diagnostics; Runtime is set for phase_activity and
// phase_event_ignored and holds the normalized backend event.
type PhaseEvent struct {
	Name       string
	Time       time.Time
	RunID      string
	WorkID     string
	Phase      string
	ResourceID string
	AgentID    string
	SessionID  string
	TurnID     string
	Elapsed    time.Duration
	Runtime    *runtime.Event
	Fields     map[string]string
}

// PhaseObserver receives phase events. It is called from the runner's
// goroutine and must be safe for concurrent use.
type PhaseObserver func(PhaseEvent)

// NewPhaseRunID returns an identifier unique to one phase invocation, so a
// reopened phase never shares correlation with its earlier run.
func NewPhaseRunID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "run_" + time.Now().UTC().Format("20060102T150405.000000000")
	}
	return "run_" + hex.EncodeToString(raw[:])
}

// Digest identifies a payload in the trace without embedding it.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// runObservation carries the correlation and progress of one phase run. It is
// owned by the runner goroutine.
type runObservation struct {
	observe      PhaseObserver
	request      *PhaseRequest
	agentID      string
	sessionID    string
	turnID       string
	started      time.Time
	lastActivity time.Time
	stage        string
	lastKind     string
	lastSummary  string
	lastMethod   string
	events       int
	ignoredCount int
	deltas       int
	streaming    bool
	openItems    map[string]string
	reported     bool
}

func newRunObservation(observe PhaseObserver, request *PhaseRequest) *runObservation {
	if request.RunID == "" {
		request.RunID = NewPhaseRunID()
	}
	now := time.Now()
	return &runObservation{observe: observe, request: request, started: now, lastActivity: now, stage: StageResolving, openItems: map[string]string{}}
}

func (o *runObservation) advance(stage string) {
	o.stage = stage
	o.lastActivity = time.Now()
}

func (o *runObservation) sinceActivity() time.Duration { return time.Since(o.lastActivity) }

func (o *runObservation) emit(name string, fields map[string]string) {
	o.emitEvent(name, nil, fields)
}

func (o *runObservation) emitEvent(name string, event *runtime.Event, fields map[string]string) {
	if o.observe == nil {
		return
	}
	now := time.Now()
	turnID := o.turnID
	if event != nil && event.TurnID != "" {
		turnID = event.TurnID
	}
	o.observe(PhaseEvent{
		Name:       name,
		Time:       now,
		RunID:      o.request.RunID,
		WorkID:     o.request.WorkID,
		Phase:      string(o.request.Phase),
		ResourceID: o.request.ResourceID,
		AgentID:    o.agentID,
		SessionID:  o.sessionID,
		TurnID:     turnID,
		Elapsed:    now.Sub(o.started),
		Runtime:    event,
		Fields:     fields,
	})
}

// activity records one accepted runtime event. Streamed message deltas are
// counted rather than traced one by one; the first delta of each stream is
// traced so the start of a response is visible.
func (o *runObservation) activity(event runtime.Event, method string) {
	o.lastActivity = time.Now()
	o.events++
	if o.stage == StageTurnRequested || o.stage == StageTurnStarted {
		o.stage = StageActive
	}
	o.lastKind, o.lastMethod = string(event.Kind), method
	o.trackItem(event, method)
	if event.Kind == runtime.KindMessage && strings.Contains(method, "delta") {
		o.deltas++
		if o.streaming {
			return
		}
		o.streaming = true
		o.lastSummary = "response streaming"
		o.emitEvent(PhaseActivity, &event, map[string]string{"kind": string(event.Kind), "backend_method": method, "note": "response stream started"})
		return
	}
	o.streaming = false
	o.lastSummary = event.Summary
	o.emitEvent(PhaseActivity, &event, map[string]string{"kind": string(event.Kind), "backend_method": method, "open_items": strconv.Itoa(len(o.openItems))})
}

// trackItem follows backend items (commands, tool calls, file edits) from
// start to completion, so a hung tool is named in failure diagnostics.
func (o *runObservation) trackItem(event runtime.Event, method string) {
	if method != "item/started" && method != "item/completed" {
		return
	}
	var p struct {
		ItemID string `json:"itemId"`
		Item   struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	if json.Unmarshal(event.Raw, &p) != nil {
		return
	}
	id := p.Item.ID
	if id == "" {
		id = p.ItemID
	}
	if id == "" {
		return
	}
	if method == "item/started" {
		o.openItems[id] = string(event.Kind) + ": " + event.Summary
	} else {
		delete(o.openItems, id)
	}
}

func (o *runObservation) ignored(event runtime.Event, reason, expected string) {
	o.ignoredCount++
	o.emitEvent(PhaseEventIgnored, &event, map[string]string{
		"reason":         reason,
		"expected":       expected,
		"event_session":  event.SessionID,
		"event_turn":     event.TurnID,
		"kind":           string(event.Kind),
		"backend_method": event.Metadata["backend_method"],
	})
}

// diagnostics is the failure context shared by every terminal failure event.
func (o *runObservation) diagnostics(extra map[string]string) map[string]string {
	fields := map[string]string{
		"stage":                       o.stage,
		"time_since_last_activity_ms": strconv.FormatInt(o.sinceActivity().Milliseconds(), 10),
		"last_event_kind":             o.lastKind,
		"last_event_method":           o.lastMethod,
		"last_event_summary":          o.lastSummary,
		"events":                      strconv.Itoa(o.events),
		"message_deltas":              strconv.Itoa(o.deltas),
		"ignored_events":              strconv.Itoa(o.ignoredCount),
		"open_items":                  strconv.Itoa(len(o.openItems)),
	}
	if len(o.openItems) > 0 {
		pending := make([]string, 0, len(o.openItems))
		for _, summary := range o.openItems {
			pending = append(pending, summary)
		}
		sort.Strings(pending)
		fields["open_item_summaries"] = strings.Join(pending, " | ")
	}
	for key, value := range extra {
		fields[key] = value
	}
	return fields
}

// fail emits a terminal failure event with full diagnostics. finish does not
// emit a second failure event for the same run.
func (o *runObservation) fail(name string, extra map[string]string) {
	o.emit(name, o.diagnostics(extra))
	o.reported = true
}

func (o *runObservation) finish(ctx context.Context, err error) {
	fields := map[string]string{"stage": o.stage, "outcome": "ok"}
	if err != nil {
		switch {
		case o.reported:
		case errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil:
			deadline := ""
			if d, ok := ctx.Deadline(); ok {
				deadline = d.Format(time.RFC3339)
			}
			o.fail(PhaseTimeout, map[string]string{"deadline": deadline})
		case errors.Is(err, context.Canceled) && ctx.Err() != nil:
			o.fail(PhaseCanceled, nil)
		}
		fields["outcome"] = "error"
		fields["error"] = err.Error()
	}
	o.emit(PhaseFinished, fields)
}
