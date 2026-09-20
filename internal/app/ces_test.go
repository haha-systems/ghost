package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
	"github.com/haha-systems/ghost/internal/ui/dashboard"
)

// The whole controlled run: one artifact per phase, each in its own session.
var cesRunArtifacts = []string{
	`{"classification":"defect","unknowns":[{"local_ref":"u1","question":"which layer drops the sequence"}],"next_investigation":"capture the sequence at the boundary"}`,
	`{"hypotheses":[{"local_ref":"h1","mechanism":"the sequence is dropped before rendering","falsifier":"a capture showing it arriving"}],"leading_hypothesis_ref":"h1"}`,
	`{"frame":{"local_ref":"f1","name":"carry the sequence","summary":"make the sequence visible at the boundary","completion_conditions":["the sequence renders"]}}`,
	`{"actions":[{"local_ref":"a1","description":"carry the sequence through the boundary"}],"outcomes":[{"local_ref":"x1","description":"the sequence renders"}]}`,
	`{"residual_uncertainty":"only the happy path was exercised","completion_recommended":true}`,
}

func cesModel(t *testing.T, artifacts ...string) (Model, *scriptedRuntime, map[string]*fake.Session) {
	t.Helper()
	cfg := appQACConfig()
	m := New(cfg, nil)
	rt := &scriptedRuntime{outputs: artifacts}
	m.phaseRuntime = rt
	sessions := map[string]*fake.Session{"wraith": fake.NewSession("wraith"), "shade": fake.NewSession("shade")}
	m.sessions = map[string]runtime.Session{"wraith": sessions["wraith"], "shade": sessions["shade"]}
	return m, rt, sessions
}

// Submitting work must produce a visible CES state before any phase finishes,
// and must not hand the whole task to a persistent agent session.
func TestNewTaskStartsCESTriageAndSpareThePersistentSessions(t *testing.T) {
	m, rt, sessions := cesModel(t, cesRunArtifacts...)
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "make VisibleThroughSeq visible"})
	view, ok := m.OperatorView()
	if !ok || view.WorkStatus != epistemic.WorkActive || view.Phase != epistemic.PhaseTriage {
		t.Fatalf("operator view = %#v available=%t", view, ok)
	}
	if m.cognition.Work() == nil || m.cognition.Work().OwnerResource != "wraith" {
		t.Fatalf("qac work = %#v", m.cognition.Work())
	}
	if agent, ok := m.dashboard.Agent("wraith"); !ok || agent.State != ghostmodel.AgentActive {
		t.Fatalf("qac resource roster state = %#v, want active", agent)
	}
	for id, session := range sessions {
		if session.LastSend != "" {
			t.Fatalf("persistent session %q received task work: %q", id, session.LastSend)
		}
	}
	if cmd == nil {
		t.Fatal("triage was not scheduled")
	}
	if len(rt.started()) != 0 {
		t.Fatal("a phase session was started before the command ran")
	}
}

func TestCESPhaseActivityUsesRosterIdentityWithoutReplacingPersistentSession(t *testing.T) {
	m, _, _ := cesModel(t, cesRunArtifacts...)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m.dashboard = m.dashboard.UpdateAgentMetadata("wraith", "scripted", "persistent-wraith")
	m, _ = updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "make activity visible"})
	runID := m.cesRunID
	m, _ = updateModel(t, m, phaseEventMsg{event: cognition.PhaseEvent{
		Name: cognition.PhaseActivity, RunID: runID, WorkID: string(m.cesStore.State().Task.ID),
		Phase: string(epistemic.PhaseTriage), ResourceID: "wraith", AgentID: "wraith",
		SessionID: "phase-session-1", TurnID: "phase-turn-1",
		Runtime: &runtime.Event{Kind: runtime.KindCommand, Summary: "go test ./internal/app", SessionID: "phase-session-1", TurnID: "phase-turn-1"},
	}})
	m, _ = updateModel(t, m, phaseEventMsg{event: cognition.PhaseEvent{
		Name: cognition.PhaseFinished, RunID: runID, WorkID: string(m.cesStore.State().Task.ID),
		Phase: string(epistemic.PhaseTriage), ResourceID: "wraith", AgentID: "wraith",
		SessionID: "phase-session-1", TurnID: "phase-turn-1", Fields: map[string]string{"outcome": "ok"},
	}})
	agent, ok := m.dashboard.Agent("wraith")
	if !ok || agent.State != ghostmodel.AgentIdle || agent.SessionID != "persistent-wraith" {
		t.Fatalf("phase completion changed roster identity: %#v", agent)
	}
	m = openAgent(t, m, "wraith")
	if !strings.Contains(m.View().Content, "go test ./internal/app") {
		t.Fatal("phase activity is missing from the mapped agent detail view")
	}
	entries := m.history.Agent("wraith")
	found := false
	for _, entry := range entries {
		if entry.Message == "go test ./internal/app" {
			found = entry.SessionID == "phase-session-1" && entry.Meta("phase_run_id") == runID
		}
	}
	if !found {
		t.Fatal("phase activity did not retain ephemeral session and phase-run correlation")
	}
}

// A controlled run walks TRIAGE -> ABDUCE -> FRAME -> EXECUTE -> CLOSE, each in
// a distinct session carrying only the task and the CES projection.
func TestControlledRunWalksEveryPhaseInItsOwnSession(t *testing.T) {
	m, rt, sessions := cesModel(t, cesRunArtifacts...)
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "make VisibleThroughSeq visible"})
	phases := []epistemic.Phase{}
	for cmd != nil {
		if view, _ := m.OperatorView(); view.WorkStatus == epistemic.WorkActive {
			phases = append(phases, view.Phase)
		}
		m, cmd = runCmd(t, m, cmd)
	}
	want := []epistemic.Phase{epistemic.PhaseTriage, epistemic.PhaseAbduce, epistemic.PhaseFrame, epistemic.PhaseExecute, epistemic.PhaseClose}
	if fmt.Sprint(phases) != fmt.Sprint(want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
	started := rt.started()
	if len(started) != len(want) {
		t.Fatalf("phase sessions = %d, want %d", len(started), len(want))
	}
	ids := map[string]bool{}
	for i, session := range started {
		if ids[session.id] {
			t.Fatalf("session %q was reused", session.id)
		}
		ids[session.id] = true
		phase := strings.ToUpper(string(want[i]))
		if !strings.Contains(session.config.Instructions, "CES PHASE: "+phase) {
			t.Fatalf("session %d instructions do not carry the %s contract", i, phase)
		}
		for previous := 0; previous < i; previous++ {
			if strings.Contains(session.input, cesRunArtifacts[previous]) || strings.Contains(session.input, started[previous].id) {
				t.Fatalf("session %d received phase %d's transcript: %q", i, previous, session.input)
			}
		}
	}
	// ABDUCE must see the task and the CES projection, not the triage session.
	if !strings.Contains(started[1].input, "make VisibleThroughSeq visible") || !strings.Contains(started[1].input, "which layer drops the sequence") {
		t.Fatalf("abduce input = %q", started[1].input)
	}
	view, _ := m.OperatorView()
	if view.WorkStatus == epistemic.WorkActive {
		t.Fatalf("work did not terminate: %#v", view)
	}
	owner := m.phaseAgentID(m.cesResource, "")
	if agent, ok := m.dashboard.Agent(owner); !ok || agent.State != ghostmodel.AgentDone {
		t.Fatalf("terminal CES resource state = %#v, want done", agent)
	}
	for id, session := range sessions {
		if session.LastSend != "" {
			t.Fatalf("persistent session %q received task work: %q", id, session.LastSend)
		}
	}
}

func TestCESResourceHandoffMovesRosterActivityBetweenMappedAgents(t *testing.T) {
	m, _, _ := cesModel(t, cesRunArtifacts...)
	m, _ = updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "handoff resource"})
	workID := string(m.cesStore.State().Task.ID)
	firstRun := m.cesRunID
	m, _ = updateModel(t, m, phaseEventMsg{event: cognition.PhaseEvent{
		Name: cognition.PhaseFinished, RunID: firstRun, WorkID: workID,
		Phase: string(epistemic.PhaseTriage), ResourceID: "wraith", AgentID: "wraith",
		Fields: map[string]string{"outcome": "ok"},
	}})
	m.cesResource = "shade"
	m.cesRunID = "handoff-run"
	m, _ = updateModel(t, m, phaseEventMsg{event: cognition.PhaseEvent{
		Name: cognition.PhaseSessionStarted, RunID: m.cesRunID, WorkID: workID,
		Phase: string(epistemic.PhaseAbduce), ResourceID: "shade", AgentID: "shade",
		SessionID: "phase-session-2",
	}})
	wraith, _ := m.dashboard.Agent("wraith")
	shade, _ := m.dashboard.Agent("shade")
	if wraith.State != ghostmodel.AgentIdle || shade.State != ghostmodel.AgentActive {
		t.Fatalf("handoff roster states = %s/%s, want idle/active", wraith.State, shade.State)
	}
}

// A malformed artifact ends the work explicitly. It must never fall back to
// sending the task to a persistent session.
func TestMalformedArtifactEndsWorkWithoutFallingBack(t *testing.T) {
	m, _, sessions := cesModel(t, `{"classification":"defect","unexpected":true}`)
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "make VisibleThroughSeq visible"})
	m, _ = runCmd(t, m, cmd)
	view, _ := m.OperatorView()
	if view.WorkStatus != epistemic.WorkIncomplete {
		t.Fatalf("work status = %q, want incomplete", view.WorkStatus)
	}
	if m.cesStore.State().Task.TerminalReason != "phase_execution_failed" {
		t.Fatalf("terminal reason = %q", m.cesStore.State().Task.TerminalReason)
	}
	if m.cognition.Work().State != cognition.WorkIncomplete {
		t.Fatalf("qac work = %#v", m.cognition.Work())
	}
	for id, session := range sessions {
		if session.LastSend != "" {
			t.Fatalf("persistent session %q received task work: %q", id, session.LastSend)
		}
	}
}

// The operator sees each transition as it happens, including the reason a
// backward transition was taken.
func TestPhaseTransitionsReachTheOperator(t *testing.T) {
	m, _, _ := cesModel(t, cesRunArtifacts...)
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "make VisibleThroughSeq visible"})
	for cmd != nil {
		m, cmd = runCmd(t, m, cmd)
	}
	transitions := 0
	for _, entry := range m.history.All() {
		if entry.Kind == event.KindPhase {
			transitions++
			if entry.Meta("to") == "" {
				t.Fatalf("phase event has no destination: %#v", entry)
			}
		}
	}
	if transitions < 4 {
		t.Fatalf("phase transitions in history = %d, want at least 4", transitions)
	}
}

// Steering while CES work is in flight is evidence, not a dispatch.
func TestSteeringDuringCESWorkDoesNotReachPersistentSessions(t *testing.T) {
	m, _, sessions := cesModel(t, cesRunArtifacts...)
	m, _ = updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "make VisibleThroughSeq visible"})
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "also check the footer"})
	if cmd != nil {
		t.Fatal("steering during CES work scheduled a dispatch")
	}
	for id, session := range sessions {
		if session.LastSend != "" || session.LastSteer != "" {
			t.Fatalf("persistent session %q received steering: %q / %q", id, session.LastSend, session.LastSteer)
		}
	}
}

// scriptedRuntime hands each phase session one canned artifact. It stands in
// for the codex runtime so a test never starts a real cognitive session.
type scriptedRuntime struct {
	mu       sync.Mutex
	outputs  []string
	sessions []*scriptedSession
}

func (r *scriptedRuntime) Name() string                       { return "scripted" }
func (r *scriptedRuntime) Capabilities() runtime.Capabilities { return runtime.Capabilities{} }
func (r *scriptedRuntime) Close() error                       { return nil }
func (r *scriptedRuntime) started() []*scriptedSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*scriptedSession(nil), r.sessions...)
}
func (r *scriptedRuntime) Start(_ context.Context, config runtime.SessionConfig) (runtime.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := len(r.sessions)
	if index >= len(r.outputs) {
		return nil, fmt.Errorf("no scripted output %d", index)
	}
	session := &scriptedSession{config: config, id: fmt.Sprintf("phase-session-%d", index+1), output: r.outputs[index], events: make(chan runtime.Event, 2)}
	r.sessions = append(r.sessions, session)
	return session, nil
}

type scriptedSession struct {
	config runtime.SessionConfig
	id     string
	output string
	events chan runtime.Event
	input  string
}

func (s *scriptedSession) ID() string                  { return s.id }
func (s *scriptedSession) State() runtime.SessionState { return runtime.StateIdle }
func (s *scriptedSession) Send(_ context.Context, input runtime.Input) error {
	s.input = input.Text
	s.events <- runtime.Event{SessionID: s.id, TurnID: "turn-1", Kind: runtime.KindMessage, Summary: s.output, Metadata: map[string]string{"backend_method": "item/agentMessage/delta"}}
	s.events <- runtime.Event{SessionID: s.id, TurnID: "turn-1", Kind: runtime.KindStatus, Metadata: map[string]string{"backend_method": "turn/completed"}}
	return nil
}
func (s *scriptedSession) Steer(context.Context, runtime.Input) error {
	return fmt.Errorf("unexpected steer")
}
func (s *scriptedSession) Interrupt(context.Context) error { return fmt.Errorf("unexpected interrupt") }
func (s *scriptedSession) Events() <-chan runtime.Event    { return s.events }
func (s *scriptedSession) Stats() runtime.SessionStats     { return runtime.SessionStats{} }
func (s *scriptedSession) Metadata() runtime.SessionMetadata {
	return runtime.SessionMetadata{ThreadID: s.id, Model: "scripted"}
}
func (s *scriptedSession) Close() error { return nil }

var _ tea.Cmd = tea.Cmd(nil)

// An execution artifact that contradicts its own action must route back into
// EXECUTE rather than advancing to CLOSE, and must end explicitly when the
// contradiction never clears.
func TestActiveContradictionReopensInsteadOfAdvancing(t *testing.T) {
	contradicting := `{"observations":[{"local_ref":"o1","content":"the sequence is still missing after the change"}],"actions":[{"local_ref":"a1","description":"carry the sequence through the boundary"}],"relations":[{"local_ref":"r1","kind":"contradicts","source":"o1","target":"a1"}]}`
	artifacts := append([]string{}, cesRunArtifacts[:3]...)
	for i := 0; i < 5; i++ {
		artifacts = append(artifacts, contradicting)
	}
	m, _, _ := cesModel(t, artifacts...)
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "make VisibleThroughSeq visible"})
	executions := 0
	for cmd != nil {
		if view, _ := m.OperatorView(); view.Phase == epistemic.PhaseExecute && view.WorkStatus == epistemic.WorkActive {
			executions++
		}
		m, cmd = runCmd(t, m, cmd)
	}
	view, _ := m.OperatorView()
	if view.Phase == epistemic.PhaseClose {
		t.Fatal("an active contradiction advanced into CLOSE")
	}
	if executions < 2 {
		t.Fatalf("execute ran %d times, want the contradiction to route back into it", executions)
	}
	if view.WorkStatus != epistemic.WorkIncomplete {
		t.Fatalf("work status = %q, want incomplete", view.WorkStatus)
	}
	contradictions := 0
	for _, entry := range m.history.All() {
		if entry.Kind == event.KindContradict {
			contradictions++
		}
	}
	if contradictions == 0 {
		t.Fatal("no contradiction reached the operator")
	}
}

// A phase runner failure ends the work; it never re-enters the monolithic path.
func TestPhaseRunnerFailureDoesNotFallBackToDispatch(t *testing.T) {
	m, _, sessions := cesModel(t)
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "make VisibleThroughSeq visible"})
	m, next := runCmd(t, m, cmd)
	if next != nil {
		t.Fatal("a failed phase scheduled more work")
	}
	view, _ := m.OperatorView()
	if view.WorkStatus != epistemic.WorkIncomplete {
		t.Fatalf("work status = %q, want incomplete", view.WorkStatus)
	}
	for id, session := range sessions {
		if session.LastSend != "" {
			t.Fatalf("persistent session %q received task work: %q", id, session.LastSend)
		}
	}
}

func TestCompleteCESWorkAllowsTheNextGlobalSubmission(t *testing.T) {
	artifacts := append(append([]string{}, cesRunArtifacts...), cesRunArtifacts...)
	m, rt, _ := cesModel(t, artifacts...)
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "first work"})
	for cmd != nil {
		m, cmd = runCmd(t, m, cmd)
	}
	first := m.cesStore
	firstID := first.State().Task.ID
	if first.State().Task.Status != epistemic.WorkComplete {
		t.Fatalf("first status = %q, want complete", first.State().Task.Status)
	}

	m, cmd = updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "second work"})
	if cmd == nil {
		t.Fatal("second submission did not schedule triage")
	}
	if m.cesStore == first || m.cesStore.State().Task.ID == firstID {
		t.Fatal("second submission reused the terminal CES store")
	}
	if m.cesStore.State().Task.Status != epistemic.WorkActive || m.cesStore.State().Task.Phase != epistemic.PhaseTriage {
		t.Fatalf("second task = %#v", m.cesStore.State().Task)
	}
	if first.State().Task.Status != epistemic.WorkComplete || first.State().Task.TerminalReason != "" {
		t.Fatalf("first terminal task changed: %#v", first.State().Task)
	}
	if len(rt.started()) != len(cesRunArtifacts) {
		t.Fatalf("second triage started before command execution: %d sessions", len(rt.started()))
	}
}

func TestIncompleteCESWorkAllowsTheNextGlobalSubmission(t *testing.T) {
	m, rt, _ := cesModel(t, `{"classification":"defect","unexpected":true}`, cesRunArtifacts[0])
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "first work"})
	m, next := runCmd(t, m, cmd)
	if next != nil || m.cesStore.State().Task.Status != epistemic.WorkIncomplete {
		t.Fatalf("first task = %#v, next=%v", m.cesStore.State().Task, next)
	}
	first := m.cesStore

	m, cmd = updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "second work"})
	if cmd == nil || m.cesStore == first {
		t.Fatal("second submission did not create a fresh CES work item")
	}
	if m.cesStore.State().Task.Status != epistemic.WorkActive || m.cesStore.State().Task.Phase != epistemic.PhaseTriage {
		t.Fatalf("second task = %#v", m.cesStore.State().Task)
	}
	if first.State().Task.Status != epistemic.WorkIncomplete || first.State().Task.TerminalReason != "phase_execution_failed" {
		t.Fatalf("first terminal task changed: %#v", first.State().Task)
	}
	if len(rt.started()) != 1 {
		t.Fatalf("second triage started before command execution: %d sessions", len(rt.started()))
	}
}

func TestLatePhaseResultCannotMutateNewCESWork(t *testing.T) {
	m, rt, _ := cesModel(t, cesRunArtifacts[0], cesRunArtifacts[0])
	m, cmd := updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "first work"})
	if cmd == nil {
		t.Fatal("first triage was not scheduled")
	}
	stale := cmd()
	oldID := m.cesStore.State().Task.ID
	if m.cesTerminal(epistemic.WorkIncomplete, "phase_execution_failed", "") != nil {
		t.Fatal("terminal cleanup returned a command")
	}
	m, cmd = updateModel(t, m, dashboard.SteeringSubmittedMsg{Text: "second work"})
	if cmd == nil || m.cesStore.State().Task.ID == oldID {
		t.Fatal("second work was not created")
	}
	newID := m.cesStore.State().Task.ID
	m, next := updateModel(t, m, stale)
	if next != nil {
		t.Fatal("stale phase result scheduled work")
	}
	if m.cesStore.State().Task.ID != newID || m.cesStore.State().Task.Status != epistemic.WorkActive || m.cesStore.State().Task.Phase != epistemic.PhaseTriage {
		t.Fatalf("stale result mutated new task = %#v", m.cesStore.State().Task)
	}
	if len(rt.started()) != 1 {
		t.Fatalf("unexpected phase session count after stale result: %d", len(rt.started()))
	}
}
