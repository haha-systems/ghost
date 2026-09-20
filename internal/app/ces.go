package app

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	ghostmodel "github.com/haha-systems/ghost/internal/model"
	"github.com/haha-systems/ghost/internal/runtime"
	uiEpistemic "github.com/haha-systems/ghost/internal/ui/epistemic"
)

// This file is the live CES driver. Ghost owns phase progression: the store
// validates each artifact, the orchestrator chooses the next phase, QAC chooses
// only the resource, and the phase runner executes each phase in a fresh
// session. Nothing here sends task work to the persistent agent sessions, and
// nothing here falls back to dispatchCognition.

// cesPhaseTimeout bounds one cognitive phase. A phase that never returns would
// otherwise strand the work in ACTIVE with no way back to the operator.
const cesPhaseTimeout = 30 * time.Minute

// cesMaxPhaseRepeats bounds how many times one phase may run back to back. A
// contradiction that routes to the phase that produced it would otherwise spin.
const cesMaxPhaseRepeats = 3

// cesPhaseResultMsg carries one phase execution back into the Bubble Tea loop.
// Phases run in a command, so Update never blocks on a cognitive session.
type cesPhaseResultMsg struct {
	phase    epistemic.Phase
	resource string
	run      cesRun
	result   cognition.PhaseResult
	err      error
}

// cesActive reports whether CES controls an active work item. A terminal store
// remains attached for inspection, but it no longer owns steering or phase
// execution, so the next submission may open a fresh work item.
func (m Model) cesActive() bool {
	return m.cesStore != nil && m.cesStore.State().Task.Status == epistemic.WorkActive
}

// startCESWork opens a CES store for a new task and runs its first phase. The
// store is created first: if CES cannot be initialized there is no CES work,
// rather than work that quietly runs without it.
func (m *Model) startCESWork(goal string) tea.Cmd {
	store, err := epistemic.NewStore(goal)
	if err != nil {
		m.appendEvent(event.Event{Source: "CES", Kind: event.KindError, Message: "start work: " + err.Error()})
		return nil
	}
	plan, err := m.cognition.StartWork(goal)
	if err != nil {
		m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()})
		return nil
	}
	if err := m.cognition.Commit(plan, timeNow()); err != nil {
		m.appendEvent(event.Event{Source: "QAC", Kind: event.KindError, Message: err.Error()})
		return nil
	}
	m.cesStore = store
	m.orchestrator = cognition.NewOrchestrator(store)
	m.cesResource = plan.To
	m.cesPhase = store.State().Task.Phase
	m.cesRepeats = 1
	m.cesPublished = 0
	m.publishCES()
	return m.runPhase(plan)
}

// runPhase executes the store's current phase on the resource QAC selected.
// The projection is taken here, on the UI goroutine, so the command touches
// no CES state.
func (m *Model) runPhase(plan cognition.Plan) tea.Cmd {
	if m.cesStore == nil || m.orchestrator == nil {
		return nil
	}
	task := m.cesStore.State().Task
	if task.Status != epistemic.WorkActive {
		return nil
	}
	resource := plan.To
	run := m.newCESRun(task.Phase, resource)
	m.cesRunID = run.id
	m.setCESAgentState(ghostmodel.AgentActive, fmt.Sprintf("%s phase starting", strings.ToLower(string(task.Phase))))
	m.traceQACSelection(run, plan)
	if !plan.Initial {
		m.traceCES(run, cognition.PhaseNextStarted, strings.ToUpper(string(task.Phase)), "", "", map[string]string{"resource": resource})
	}
	if m.phaseRuntime == nil {
		m.traceCES(run, "phase_dispatch_failed", "no phase runtime", "", "", nil)
		return m.cesTerminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", "no phase runtime is configured")
	}
	projection, err := m.cesStore.Project(epistemic.ProjectionRequest{Phase: task.Phase})
	if err != nil {
		m.traceCES(run, "phase_dispatch_failed", "projection", "", "", map[string]string{"error": err.Error()})
		return m.cesTerminal(epistemic.WorkIncomplete, "phase_execution_failed", "projection: "+err.Error())
	}
	if _, ok := m.cognition.Agent(resource); !ok {
		m.traceCES(run, "phase_dispatch_failed", "unknown resource", "", "", nil)
		return m.cesTerminal(epistemic.WorkIncomplete, "cognitive_resource_unavailable", fmt.Sprintf("unknown cognitive resource %q", resource))
	}
	m.cesResource = resource
	request := cognition.PhaseRequest{
		WorkID:     string(task.ID),
		Goal:       task.Goal,
		Phase:      task.Phase,
		ResourceID: resource,
		Projection: projection,
		RunID:      run.id,
	}
	m.traceCES(run, cognition.PhaseScheduled, "resource="+strings.ToUpper(resource), "", "", map[string]string{
		"repeat":             strconv.Itoa(m.cesRepeats),
		"reopen_count":       strconv.Itoa(task.ReopenCount),
		"projection_objects": strconv.Itoa(len(projection.IDs)),
		"timeout_ms":         strconv.FormatInt(cesPhaseTimeout.Milliseconds(), 10),
		"stall_after_ms":     strconv.FormatInt(m.config.Trace.StallAfter().Milliseconds(), 10),
	})
	m.record("", event.Event{Source: "CES", Kind: event.KindStatus, Message: fmt.Sprintf("%s phase running on %s", strings.ToUpper(string(task.Phase)), strings.ToUpper(resource))}, map[string]string{"phase": string(task.Phase), "resource": resource, "phase_run_id": run.id})
	m.publishCES()
	runner := cognition.NewPhaseRunner(m.phaseRuntime, m.phaseSessionConfig).
		WithObserver(phaseObserver(m.trace, m.enqueuePhaseEvent)).
		WithStallTimeout(m.config.Trace.StallAfter())
	phase := task.Phase
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cesPhaseTimeout)
		defer cancel()
		result, err := runner.Run(ctx, request)
		return cesPhaseResultMsg{phase: phase, resource: resource, run: run, result: result, err: err}
	}
}

// phaseSessionConfig resolves a QAC resource to the runtime configuration of a
// fresh session. The phase runner appends the phase contract to it.
func (m Model) phaseSessionConfig(_ context.Context, resource string, _ epistemic.Phase) (runtime.SessionConfig, error) {
	if m.cognition == nil {
		return runtime.SessionConfig{}, fmt.Errorf("cognition is not configured")
	}
	agent, ok := m.cognition.Agent(resource)
	if !ok {
		return runtime.SessionConfig{}, fmt.Errorf("unknown cognitive resource %q", resource)
	}
	if m.config.Agents == nil {
		return runtime.SessionConfig{}, fmt.Errorf("no agents are configured")
	}
	backend, ok := (*m.config.Agents)[agent]
	if !ok {
		return runtime.SessionConfig{}, fmt.Errorf("resource %q has no agent configuration", resource)
	}
	return makeSessionConfig(m.config.Global.InitialPrompt, agent, backend)
}

// handlePhaseResult commits one phase artifact, lets the orchestrator choose
// the next phase, and lets QAC choose the resource that runs it. Every failure
// on this path is terminal for the work; none of them returns to the old path.
func (m Model) handlePhaseResult(msg cesPhaseResultMsg) (Model, tea.Cmd) {
	if m.cesStore == nil || m.orchestrator == nil || !m.cesActive() {
		return m, nil
	}
	if msg.run.workID == "" || msg.run.workID != string(m.cesStore.State().Task.ID) {
		return m, nil
	}
	controller := cesController{store: m.cesStore, orchestrator: m.orchestrator, coordinator: m.cognition, sessions: m.sessions, phase: &m.cesPhase, repeats: &m.cesRepeats}
	outcome := controller.process(context.Background(), msg, cesControllerHooks{
		newRun:            m.newCESRun,
		trace:             m.traceCES,
		traceQACSelection: m.traceQACSelection,
		traceTransition:   m.traceTransition,
		record:            m.record,
		publish:           m.publishCES,
		qacMeta:           m.qacMeta,
	})
	if outcome.terminal != nil {
		task := m.cesStore.State().Task
		if outcome.terminal.detail != "" {
			m.record("", event.Event{Source: "CES", Kind: event.KindError, Message: outcome.terminal.detail}, map[string]string{"reason": task.TerminalReason})
		}
		m.finishCESWork(task)
		return m, nil
	}
	if outcome.plan != nil {
		return m, m.runPhase(*outcome.plan)
	}
	return m, nil

}

// cesQACRequestFields records the advisory resource request an artifact made
// for the phase CES already chose.
func cesQACRequestFields(request cognition.QACRequest, next epistemic.Phase) map[string]string {
	return map[string]string{
		"requested_phase": string(next),
		"direction":       request.Direction,
		"uncertainty":     strconv.FormatFloat(request.Uncertainty, 'f', 2, 64),
		"novelty":         strconv.FormatFloat(request.Novelty, 'f', 2, 64),
		"expected_gain":   strconv.FormatFloat(request.ExpectedGain, 'f', 2, 64),
		"failed_attempts": strconv.Itoa(request.FailedAttempts),
		"reason":          request.Reason,
	}
}

// cesTerminal ends CES work with an explicit reason and publishes it. CES work
// never ends silently and never degrades into the old execution path.
func (m *Model) cesTerminal(status epistemic.WorkStatus, reason, detail string) tea.Cmd {
	if m.cesStore == nil || m.orchestrator == nil {
		return nil
	}
	if task := m.cesStore.State().Task; task.Status == epistemic.WorkActive {
		if _, err := m.cesStore.SetTerminal(status, reason); err != nil {
			m.appendEvent(event.Event{Source: "CES", Kind: event.KindError, Message: "terminate: " + err.Error()})
		}
	}
	task := m.cesStore.State().Task
	if detail != "" {
		m.record("", event.Event{Source: "CES", Kind: event.KindError, Message: detail}, map[string]string{"reason": task.TerminalReason})
	}
	m.finishCESWork(task)
	return nil
}

// finishCESWork mirrors a terminal CES status into the QAC resource view and
// the operator surfaces. CES owns the status; QAC only learns it.
func (m *Model) finishCESWork(task epistemic.Task) {
	m.setCESAgentState(cesTerminalAgentState(task.Status), "CES "+strings.ToLower(string(task.Status)))
	m.cesRunID = ""
	if m.cognition != nil {
		m.cognition.SyncWorkStatus(task.Status, task.TerminalReason, timeNow())
	}
	m.publishCES()
}

// publishCES is the single point at which CES state reaches the operator. It
// refreshes the work header, the phase, the frontier, the inspector snapshot,
// and any semantic events the store produced since the last publish.
func (m *Model) publishCES() {
	if m.cesStore == nil {
		return
	}
	view := m.cesStore.OperatorView()
	state := m.cesStore.State()
	snapshot := cesSnapshot(state)
	snapshot.Supplement.TerminalReason = state.Task.TerminalReason
	*m = m.SetEpistemicView(view, m.cesResource, snapshot)
	m.syncWork()
	semantic := m.cesStore.SemanticEvents()
	start := m.cesPublished
	if start > len(semantic) {
		start = len(semantic)
	}
	for _, item := range semantic[start:] {
		entry := cesSemanticEvent(item, state, view)
		m.record("", entry, map[string]string{"semantic_kind": string(item.Kind)})
		m.epistemic = m.epistemic.AppendEvent(entry)
	}
	m.cesPublished = len(semantic)
}

// cesSemanticEvent renders one CES semantic event for the operator. Backward
// transitions carry their reason so a reopen is never unexplained.
func cesSemanticEvent(item epistemic.SemanticEvent, state epistemic.State, view epistemic.OperatorView) event.Event {
	meta := map[string]string{}
	if item.From != "" {
		meta["from"] = string(item.From)
	}
	if item.To != "" {
		meta["to"] = string(item.To)
	}
	if item.Reason != "" {
		meta["reason"] = item.Reason
	}
	alias, summary := cesAlias(state, item.ObjectID)
	if alias != "" {
		meta["alias"] = alias
		meta["summary"] = summary
	}
	out := event.Event{Time: item.At, Source: "CES", Metadata: meta}
	switch item.Kind {
	case epistemic.SemanticPhaseChanged:
		out.Kind, out.Message = event.KindPhase, fmt.Sprintf("%s -> %s", item.From, item.To)
	case epistemic.SemanticWorkReopened:
		out.Kind, out.Message = event.KindReopen, fmt.Sprintf("reopened %s -> %s", item.From, item.To)
		meta["reopen_count"] = strconv.Itoa(view.ReopenCount)
		meta["reopen_limit"] = strconv.Itoa(epistemic.MaxReopens)
	case epistemic.SemanticHypothesisBecameLeading:
		out.Kind, out.Message = event.KindHypothesis, "leading hypothesis "+alias
		meta["status"] = "leading"
	case epistemic.SemanticHypothesisRejected:
		out.Kind, out.Message = event.KindReject, "rejected hypothesis "+alias
		meta["status"] = "rejected"
	case epistemic.SemanticFrameActivated:
		out.Kind, out.Message = event.KindFrame, "frame "+alias+" active"
	case epistemic.SemanticContradictionRecorded:
		out.Kind, out.Message = event.KindContradict, "contradiction against "+alias
	case epistemic.SemanticWorkCompleted:
		out.Kind, out.Message = event.KindComplete, "work complete"
	case epistemic.SemanticWorkIncomplete:
		out.Kind, out.Message = event.KindIncomplete, "work incomplete: "+item.Reason
	default:
		out.Kind, out.Message = event.KindCES, string(item.Kind)
	}
	return out
}

// cesSnapshot projects CES state into the read-only inspector model. The UI
// never reads the store: it renders what this projection hands it.
func cesSnapshot(state epistemic.State) uiEpistemic.Snapshot {
	snapshot := uiEpistemic.Snapshot{Operator: epistemic.OperatorViewForState(state)}
	aliases := cesAliases(state)
	add := func(id epistemic.ID, kind epistemic.ObjectKind, summary, status, falsifier string, producer epistemic.Producer, revision epistemic.ID, at time.Time) {
		snapshot.Objects = append(snapshot.Objects, uiEpistemic.Object{
			ID: id, Alias: aliases[id], Kind: kind, Label: cesLabel(summary), Summary: summary,
			Status: status, ProducedBy: cesProducer(producer), Revision: cesRevision(revision),
			CreatedAt: at, Falsifier: falsifier,
		})
	}
	for _, value := range state.Hypotheses {
		add(value.ID, epistemic.ObjectHypothesis, value.Mechanism, string(value.Status), value.Falsifier, value.ProducedBy, value.RevisionID, value.CreatedAt)
	}
	for _, value := range state.Unknowns {
		add(value.ID, epistemic.ObjectUnknown, value.Question, string(value.Status), "", value.ProducedBy, value.RevisionID, value.CreatedAt)
	}
	for _, value := range state.Frames {
		add(value.ID, epistemic.ObjectFrame, value.Summary, string(value.Status), "", value.ProducedBy, value.RevisionID, value.CreatedAt)
	}
	for _, value := range state.Claims {
		add(value.ID, epistemic.ObjectClaim, value.Text, "", "", value.ProducedBy, value.RevisionID, value.CreatedAt)
	}
	for _, value := range state.Observations {
		add(value.ID, epistemic.ObjectObservation, value.Content, "", "", value.ProducedBy, value.RevisionID, value.CreatedAt)
	}
	for _, value := range state.Constraints {
		add(value.ID, epistemic.ObjectConstraint, value.Text, "", "", value.ProducedBy, value.RevisionID, value.CreatedAt)
	}
	for _, value := range state.Actions {
		add(value.ID, epistemic.ObjectAction, value.Description, value.Status, "", value.ProducedBy, value.RevisionID, value.CreatedAt)
	}
	for _, value := range state.Outcomes {
		add(value.ID, epistemic.ObjectOutcome, value.Description, "", "", value.ProducedBy, value.RevisionID, value.CreatedAt)
	}
	summaries := map[epistemic.ID]string{}
	for _, object := range snapshot.Objects {
		summaries[object.ID] = object.Summary
	}
	for _, relation := range state.Relations {
		snapshot.Relations = append(snapshot.Relations, uiEpistemic.Relation{
			ID: relation.ID, Alias: aliases[relation.ID], Kind: string(relation.Kind),
			SourceAlias: aliases[relation.SourceID], SourceID: relation.SourceID,
			TargetAlias: aliases[relation.TargetID], TargetID: relation.TargetID,
			Status:        string(relation.Status),
			SourceSummary: summaries[relation.SourceID], TargetSummary: summaries[relation.TargetID],
			ProducedBy:       cesProducer(relation.ProducedBy),
			RetractionReason: summaries[relation.ReasonRef],
		})
	}
	return snapshot
}

// cesAliases gives every object the short, stable label the operator reads.
// CES identifiers are opaque; these are display names, not identity.
func cesAliases(state epistemic.State) map[epistemic.ID]string {
	aliases := map[epistemic.ID]string{}
	assign := func(prefix string, ids []epistemic.ID) {
		sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
		for i, id := range ids {
			aliases[id] = fmt.Sprintf("%s%d", prefix, i+1)
		}
	}
	hypotheses := make([]epistemic.ID, 0, len(state.Hypotheses))
	for _, value := range state.Hypotheses {
		hypotheses = append(hypotheses, value.ID)
	}
	unknowns := make([]epistemic.ID, 0, len(state.Unknowns))
	for _, value := range state.Unknowns {
		unknowns = append(unknowns, value.ID)
	}
	frames := make([]epistemic.ID, 0, len(state.Frames))
	for _, value := range state.Frames {
		frames = append(frames, value.ID)
	}
	claims := make([]epistemic.ID, 0, len(state.Claims))
	for _, value := range state.Claims {
		claims = append(claims, value.ID)
	}
	observations := make([]epistemic.ID, 0, len(state.Observations))
	for _, value := range state.Observations {
		observations = append(observations, value.ID)
	}
	constraints := make([]epistemic.ID, 0, len(state.Constraints))
	for _, value := range state.Constraints {
		constraints = append(constraints, value.ID)
	}
	actions := make([]epistemic.ID, 0, len(state.Actions))
	for _, value := range state.Actions {
		actions = append(actions, value.ID)
	}
	outcomes := make([]epistemic.ID, 0, len(state.Outcomes))
	for _, value := range state.Outcomes {
		outcomes = append(outcomes, value.ID)
	}
	relations := make([]epistemic.ID, 0, len(state.Relations))
	for _, value := range state.Relations {
		relations = append(relations, value.ID)
	}
	assign("H", hypotheses)
	assign("U", unknowns)
	assign("F", frames)
	assign("C", claims)
	assign("O", observations)
	assign("K", constraints)
	assign("A", actions)
	assign("X", outcomes)
	assign("R", relations)
	return aliases
}

func cesAlias(state epistemic.State, id epistemic.ID) (string, string) {
	if id == "" {
		return "", ""
	}
	alias := cesAliases(state)[id]
	for _, object := range cesSnapshot(state).Objects {
		if object.ID == id {
			return alias, object.Summary
		}
	}
	return alias, ""
}

// cesLabel shortens a summary into the one-line label the roster shows.
func cesLabel(summary string) string {
	summary = strings.TrimSpace(strings.Join(strings.Fields(summary), " "))
	if len(summary) <= 48 {
		return summary
	}
	return summary[:47] + "…"
}

func cesProducer(producer epistemic.Producer) string {
	parts := []string{}
	if producer.Artifact != "" {
		parts = append(parts, producer.Artifact)
	}
	if producer.Process != "" {
		parts = append(parts, strings.ToUpper(producer.Process))
	}
	return strings.Join(parts, " / ")
}

func cesRevision(id epistemic.ID) string {
	if len(id) <= 8 {
		return string(id)
	}
	return string(id)[:8]
}

// cesDeltaEmpty reports a delta that would record nothing. The store rejects
// an empty commit, which is the right rule for the store and the wrong outcome
// for an artifact whose only content is its narrative fields.
func cesDeltaEmpty(delta epistemic.Delta) bool {
	count := len(delta.Observations) + len(delta.Claims) + len(delta.Hypotheses) + len(delta.Unknowns) +
		len(delta.Constraints) + len(delta.Frames) + len(delta.Actions) + len(delta.Outcomes) +
		len(delta.Relations) + len(delta.Retractions) + len(delta.StatusChanges)
	return count == 0 && delta.LeadingHypothesis == "" && delta.ActiveFrame == ""
}
