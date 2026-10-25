// Package memory owns safe, bounded access to a configured memory backend.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/haha-systems/ghost/internal/core"
)

// EventSink receives memory events for the runtime to append to its event log.
type EventSink func(core.Event)

// Config controls availability, scope policy, budgets, and worker limits.
type Config struct {
	Enabled           bool
	UnavailableReason string
	Workspace         core.WorkspaceID
	WorkspaceSpace    string
	AgentSpaces       map[core.AgentID]string
	GlobalSpace       string
	ShareGlobal       bool
	AuthorizedAgents  []core.AgentID
	MaxContextTokens  int
	MaxContextRecords int
	RetrieveTimeout   time.Duration
	WriteTimeout      time.Duration
	WriteQueue        int
}

// CloseResult reports writes that could not finish before the close deadline.
type CloseResult struct {
	Lost int
}

type queuedWrite struct {
	id     string
	space  core.MemorySpace
	record core.MemoryRecord
	ready  <-chan struct{}
}

// Service enforces memory scope policy and runs one bounded write worker.
type Service struct {
	backend core.MemoryBackend
	cfg     Config
	sink    EventSink
	active  bool

	queue  chan queuedWrite
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	closed    bool
	nextWrite uint64
	pending   map[string]queuedWrite
	lost      int

	eventMu   sync.Mutex
	sequencer *core.Sequencer
}

// NewService creates a disabled no-op service unless memory is configured.
// Enabled but incomplete configuration emits memory.unavailable when possible.
func NewService(backend core.MemoryBackend, config Config, sink EventSink) *Service {
	config = normaliseConfig(config)
	s := &Service{
		backend: backend, cfg: config, sink: sink, done: make(chan struct{}),
		pending: make(map[string]queuedWrite), sequencer: core.NewSequencer(),
	}
	if !config.Enabled {
		close(s.done)
		return s
	}
	if backend == nil {
		reason := config.UnavailableReason
		if reason == "" {
			reason = "backend_unconfigured"
		}
		s.emit(core.MemoryEventUnavailable, core.AgentID(""), "", map[string]any{"reason": reason})
		close(s.done)
		return s
	}
	if config.WorkspaceSpace == "" {
		s.emit(core.MemoryEventUnavailable, core.AgentID(""), "", map[string]any{"reason": "workspace_space_missing"})
		close(s.done)
		return s
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.queue = make(chan queuedWrite, config.WriteQueue)
	s.active = true
	go s.writeWorker()
	return s
}

func normaliseConfig(config Config) Config {
	if config.MaxContextTokens <= 0 {
		config.MaxContextTokens = defaultContextTokens
	}
	if config.MaxContextRecords <= 0 {
		config.MaxContextRecords = defaultContextRecords
	}
	if config.RetrieveTimeout <= 0 {
		config.RetrieveTimeout = 2 * time.Second
	}
	if config.WriteTimeout <= 0 {
		config.WriteTimeout = 5 * time.Second
	}
	if config.WriteQueue <= 0 {
		config.WriteQueue = 64
	}
	config.AgentSpaces = cloneSpaces(config.AgentSpaces)
	config.AuthorizedAgents = append([]core.AgentID(nil), config.AuthorizedAgents...)
	return config
}

func cloneSpaces(spaces map[core.AgentID]string) map[core.AgentID]string {
	if len(spaces) == 0 {
		return nil
	}
	copy := make(map[core.AgentID]string, len(spaces))
	for agent, space := range spaces {
		copy[agent] = space
	}
	return copy
}

// Recall retrieves only policy-approved records and returns an empty package
// when the backend fails or misses its deadline.
func (s *Service) Recall(ctx context.Context, request core.MemoryRecallRequest) core.MemoryRecallResult {
	if s == nil || !s.active {
		return core.MemoryRecallResult{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now()
	requested := requestedScopes(request.Scopes)
	meta := map[string]any{
		"reason": request.Reason, "requester": request.Requester,
		"task_id": request.Task, "scopes": scopeNames(requested),
	}
	s.emit(core.MemoryEventRetrievalStarted, request.Requester, string(request.Task), meta)
	deadlineCtx, cancel := context.WithTimeout(ctx, s.cfg.RetrieveTimeout)
	defer cancel()

	targets := s.recallSpaces(request, requested)
	limit := request.TopK
	if limit <= 0 {
		limit = s.cfg.MaxContextRecords
	}
	var records []core.MemoryRecord
	filtered := 0
	violations := 0
	for _, target := range targets {
		found, err := s.backend.Retrieve(deadlineCtx, core.MemoryBackendQuery{
			Space: target, Query: request.Query, TopK: limit,
		})
		if err != nil {
			s.emit(core.MemoryEventRetrievalFailed, request.Requester, string(request.Task), map[string]any{
				"reason": request.Reason, "error_class": errorClass(err, deadlineCtx),
				"latency_ms": time.Since(started).Milliseconds(),
			})
			return core.MemoryRecallResult{}
		}
		for _, item := range found {
			item = prepareRecord(item, target)
			allowed, violation, reason := s.canRead(request, requested, item)
			if !allowed {
				filtered++
				if violation {
					violations++
				}
				s.emit(core.MemoryEventScopeFiltered, request.Requester, string(request.Task), map[string]any{
					"record_id": item.ID, "scope": item.Scope, "reason": reason,
					"scope_violation": violation,
				})
				continue
			}
			records = append(records, item)
		}
	}
	if err := deadlineCtx.Err(); err != nil {
		s.emit(core.MemoryEventRetrievalFailed, request.Requester, string(request.Task), map[string]any{
			"reason": request.Reason, "error_class": errorClass(err, deadlineCtx),
			"latency_ms": time.Since(started).Milliseconds(),
		})
		return core.MemoryRecallResult{}
	}
	records = uniqueRecords(records)
	packageResult := BuildContext(records, ContextOptions{
		MaxTokens: s.cfg.MaxContextTokens, MaxRecords: min(limit, s.cfg.MaxContextRecords), Now: time.Now(),
	})
	packageResult.Filtered = filtered
	packageResult.ScopeViolations = violations
	for _, item := range packageResult.Records {
		if item.Provenance.Validation == core.ValidationDisputed {
			packageResult.Conflicts++
		}
	}
	ids := make([]string, 0, len(packageResult.Records))
	for _, item := range packageResult.Records {
		ids = append(ids, item.ID)
	}
	payload := map[string]any{
		"reason": request.Reason, "requester": request.Requester, "task_id": request.Task,
		"latency_ms": time.Since(started).Milliseconds(), "returned": len(packageResult.Records),
		"filtered": filtered, "scope_violations": violations, "conflicts": packageResult.Conflicts,
		"injected_ids": ids, "injected_tokens": packageResult.Tokens,
		"truncated": packageResult.Truncated, "truncated_records": packageResult.TruncatedRecords,
	}
	s.emit(core.MemoryEventRetrievalCompleted, request.Requester, string(request.Task), payload)
	if packageResult.Context != "" {
		s.emit(core.MemoryEventContextInjected, request.Requester, string(request.Task), map[string]any{
			"memory_ids": ids, "tokens": packageResult.Tokens,
		})
	}
	return packageResult
}

// Enqueue normalises and queues a write without waiting for backend work.
func (s *Service) Enqueue(write core.MemoryWrite) bool {
	if s == nil || !s.active {
		return false
	}
	if strings.TrimSpace(write.Text) == "" {
		s.emit(core.MemoryEventWriteSkipped, write.Provenance.Agent, string(write.Provenance.Task), map[string]any{"reason": "empty_text", "scope": write.Scope})
		return false
	}
	if !validScope(write.Scope) {
		s.emit(core.MemoryEventWriteSkipped, write.Provenance.Agent, string(write.Provenance.Task), map[string]any{"reason": "invalid_scope", "scope": write.Scope})
		return false
	}
	provenance := write.Provenance
	if provenance.Workspace == "" {
		provenance.Workspace = string(s.cfg.Workspace)
	}
	provenance = NormalizeProvenance(provenance)
	space, reason := s.writeSpace(write.Scope, provenance)
	if reason != "" {
		s.emit(core.MemoryEventWriteSkipped, provenance.Agent, string(provenance.Task), map[string]any{
			"reason": reason, "scope": write.Scope, "claim_kind": provenance.ClaimKind,
		})
		return false
	}
	record := core.MemoryRecord{
		Scope: write.Scope, Text: write.Text, Subject: write.Subject,
		Provenance: provenance, CreatedAt: provenance.RecordedAt,
	}
	if !validScope(record.Scope) || !validClaim(record.Provenance.ClaimKind) {
		s.emit(core.MemoryEventWriteSkipped, provenance.Agent, string(provenance.Task), map[string]any{"reason": "invalid_provenance", "scope": write.Scope})
		return false
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.emitWriteEvent(core.MemoryEventWriteDropped, "", record, map[string]any{"reason": "service_closed"})
		return false
	}
	s.nextWrite++
	writeID := "write-" + strconvBase10(s.nextWrite)
	queued := queuedWrite{id: writeID, space: space, record: record}
	ready := make(chan struct{})
	queued.ready = ready
	s.pending[writeID] = queued
	select {
	case s.queue <- queued:
		s.mu.Unlock()
		s.emitWriteEvent(core.MemoryEventWriteQueued, writeID, record, map[string]any{})
		close(ready)
		return true
	default:
		delete(s.pending, writeID)
		s.mu.Unlock()
		s.emitWriteEvent(core.MemoryEventWriteDropped, writeID, record, map[string]any{"reason": "queue_full"})
		return false
	}
}

// Close drains queued writes until ctx expires. A timed-out close cancels the
// worker and reports every write that had not completed as lost.
func (s *Service) Close(ctx context.Context) (CloseResult, error) {
	if s == nil || !s.active {
		return CloseResult{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.queue)
	}
	done := s.done
	s.mu.Unlock()
	select {
	case <-done:
		return s.closeResult(), nil
	case <-ctx.Done():
		lost := s.markPendingLost()
		s.cancel()
		for _, item := range lost {
			s.emitWriteEvent(core.MemoryEventWriteLost, item.id, item.record, map[string]any{"reason": "close_deadline"})
		}
		return s.closeResult(), ctx.Err()
	}
}

func (s *Service) closeResult() CloseResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return CloseResult{Lost: s.lost}
}

func (s *Service) markPendingLost() []queuedWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]queuedWrite, 0, len(s.pending))
	for _, item := range s.pending {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
	for id := range s.pending {
		delete(s.pending, id)
	}
	s.lost += len(items)
	return items
}

func (s *Service) writeWorker() {
	defer close(s.done)
	for item := range s.queue {
		if item.ready != nil {
			select {
			case <-item.ready:
			case <-s.ctx.Done():
				continue
			}
		}
		s.mu.Lock()
		_, pending := s.pending[item.id]
		s.mu.Unlock()
		if !pending {
			continue
		}
		ctx, cancel := context.WithTimeout(s.ctx, s.cfg.WriteTimeout)
		stored, err := s.backend.Observe(ctx, core.MemoryBackendWrite{
			Space: item.space, Record: item.record, SkipAutoSupersede: true,
		})
		cancel()
		s.mu.Lock()
		_, pending = s.pending[item.id]
		delete(s.pending, item.id)
		s.mu.Unlock()
		if !pending {
			continue
		}
		if err != nil {
			s.emitWriteEvent(core.MemoryEventWriteFailed, item.id, item.record, map[string]any{"error_class": errorClass(err, ctx)})
			continue
		}
		if stored.ID == "" {
			stored.ID = item.record.ID
		}
		s.emitWriteEvent(core.MemoryEventWriteStored, item.id, item.record, map[string]any{"record_id": stored.ID})
		if item.record.Provenance.Challenges != "" {
			s.emitWriteEvent(core.MemoryEventChallengeRecorded, item.id, item.record, map[string]any{
				"record_id": stored.ID, "challenged_id": item.record.Provenance.Challenges,
			})
		}
	}
}

func (s *Service) recallSpaces(request core.MemoryRecallRequest, scopes map[core.MemoryScope]bool) []core.MemorySpace {
	if !s.authorized(request.Requester) {
		return nil
	}
	spaces := make([]core.MemorySpace, 0, len(scopes))
	byID := make(map[string]int)
	add := func(space core.MemorySpace) {
		if space.ID == "" {
			return
		}
		if index, exists := byID[space.ID]; exists {
			if space.Scope == core.MemoryScopeWorkspace {
				spaces[index] = space
			}
			return
		}
		byID[space.ID] = len(spaces)
		spaces = append(spaces, space)
	}
	if scopes[core.MemoryScopeAgent] {
		if id := s.cfg.AgentSpaces[request.Requester]; id != "" {
			add(core.MemorySpace{ID: id, Scope: core.MemoryScopeAgent, Agent: request.Requester})
		}
	}
	if scopes[core.MemoryScopeTask] {
		if request.Task != "" && s.cfg.WorkspaceSpace != "" {
			add(core.MemorySpace{ID: s.cfg.WorkspaceSpace, Scope: core.MemoryScopeTask, Task: request.Task})
		}
	}
	if scopes[core.MemoryScopeWorkspace] && s.cfg.WorkspaceSpace != "" {
		add(core.MemorySpace{ID: s.cfg.WorkspaceSpace, Scope: core.MemoryScopeWorkspace})
	}
	if scopes[core.MemoryScopeGlobal] && s.cfg.ShareGlobal && s.cfg.GlobalSpace != "" {
		add(core.MemorySpace{ID: s.cfg.GlobalSpace, Scope: core.MemoryScopeGlobal})
	}
	return spaces
}

func (s *Service) writeSpace(scope core.MemoryScope, provenance core.MemoryProvenance) (core.MemorySpace, string) {
	if !s.authorized(provenance.Agent) {
		return core.MemorySpace{}, "agent_not_authorised"
	}
	switch scope {
	case core.MemoryScopeAgent:
		if provenance.Agent == "" {
			return core.MemorySpace{}, "agent_id_missing"
		}
		space := s.cfg.AgentSpaces[provenance.Agent]
		if space == "" {
			return core.MemorySpace{}, "no_agent_space_configured"
		}
		return core.MemorySpace{ID: space, Scope: scope, Agent: provenance.Agent}, ""
	case core.MemoryScopeTask:
		if provenance.Task == "" {
			return core.MemorySpace{}, "task_id_missing"
		}
		return core.MemorySpace{ID: s.cfg.WorkspaceSpace, Scope: scope, Task: provenance.Task}, ""
	case core.MemoryScopeWorkspace:
		return core.MemorySpace{ID: s.cfg.WorkspaceSpace, Scope: scope}, ""
	case core.MemoryScopeGlobal:
		if !s.cfg.ShareGlobal || s.cfg.GlobalSpace == "" {
			return core.MemorySpace{}, "global_sharing_disabled"
		}
		return core.MemorySpace{ID: s.cfg.GlobalSpace, Scope: scope}, ""
	default:
		return core.MemorySpace{}, "invalid_scope"
	}
}

func (s *Service) canRead(request core.MemoryRecallRequest, requested map[core.MemoryScope]bool, item core.MemoryRecord) (bool, bool, string) {
	if !requested[item.Scope] {
		return false, false, "scope_not_requested"
	}
	switch item.Scope {
	case core.MemoryScopeAgent:
		if item.Provenance.Agent != request.Requester {
			return false, true, "agent_scope_owner_mismatch"
		}
		return true, false, ""
	case core.MemoryScopeTask:
		if item.Provenance.Task == "" || !taskVisible(item.Provenance.Task, request) {
			return false, true, "task_outside_request_tree"
		}
		return true, false, ""
	case core.MemoryScopeWorkspace:
		if !s.authorized(request.Requester) {
			return false, true, "agent_not_authorised"
		}
		return true, false, ""
	case core.MemoryScopeGlobal:
		if !s.cfg.ShareGlobal {
			return false, true, "global_sharing_disabled"
		}
		return true, false, ""
	default:
		return false, true, "unknown_scope"
	}
}

func taskVisible(recordTask core.TaskID, request core.MemoryRecallRequest) bool {
	if recordTask == "" || request.Task == "" {
		return false
	}
	if recordTask == request.Task {
		return true
	}
	for _, parent := range request.ParentTasks {
		if recordTask == parent {
			return true
		}
	}
	return false
}

func (s *Service) authorized(agent core.AgentID) bool {
	if agent == "" {
		return false
	}
	for _, allowed := range s.cfg.AuthorizedAgents {
		if agent == allowed {
			return true
		}
	}
	return false
}

func (s *Service) emitWriteEvent(eventType, writeID string, record core.MemoryRecord, extra map[string]any) {
	payload := map[string]any{
		"write_id": writeID, "scope": record.Scope, "claim_kind": record.Provenance.ClaimKind,
		"validation": record.Provenance.Validation, "evidence_refs": evidencePayload(record.Provenance),
		"summary": truncateRunes(sanitise(record.Text), 200),
	}
	for key, value := range extra {
		payload[key] = value
	}
	s.emit(eventType, record.Provenance.Agent, string(record.Provenance.Task), payload)
}

func (s *Service) emit(eventType string, agent core.AgentID, task string, payload any) {
	if s == nil || !s.cfg.Enabled || s.sink == nil {
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		encoded = []byte(`{}`)
	}
	id, err := core.NewID("event")
	if err != nil {
		return
	}
	actor := core.Actor{Kind: core.ActorSystem, ID: "memory"}
	event := core.Event{
		ID: core.EventID(id), SchemaVersion: core.EventSchemaVersion, Time: time.Now().UTC(),
		Workspace: s.cfg.Workspace, Actor: actor, Source: "memory", Type: eventType,
		Category: core.CategoryMemory, Payload: encoded,
	}
	if agent.Validate() == nil {
		event.AgentID = agent
		event.Actor = core.Actor{Kind: core.ActorAgent, ID: string(agent)}
	}
	if taskID := core.TaskID(task); taskID.Validate() == nil {
		event.TaskID = taskID
	}
	s.eventMu.Lock()
	sequenced, sequenceErr := s.sequencer.Assign(event)
	if sequenceErr == nil {
		s.sink(sequenced)
	}
	s.eventMu.Unlock()
}

// NormalizeProvenance applies Ghost's confidence and validation limits.
func NormalizeProvenance(provenance core.MemoryProvenance) core.MemoryProvenance {
	if !validClaim(provenance.ClaimKind) {
		provenance.ClaimKind = core.ClaimObservation
	}
	if provenance.RecordedAt.IsZero() {
		provenance.RecordedAt = time.Now().UTC()
	}
	if provenance.Validation == "" || !validValidation(provenance.Validation) {
		provenance.Validation = core.ValidationUnverified
	}
	provenance.MissingEvidence = !hasEvidence(provenance)
	cap := confidenceCap(provenance.ClaimKind, !provenance.MissingEvidence)
	if math.IsNaN(provenance.Confidence) || math.IsInf(provenance.Confidence, 0) || provenance.Confidence < 0.01 {
		provenance.Confidence = 0.01
	}
	if provenance.Confidence > cap {
		provenance.Confidence = cap
	}
	if provenance.ClaimKind != core.ClaimVerifiedResult || provenance.MissingEvidence {
		if provenance.Validation == core.ValidationVerified {
			provenance.Validation = core.ValidationUnverified
		}
	}
	if provenance.ClaimKind == core.ClaimVerifiedResult && provenance.MissingEvidence {
		provenance.Validation = core.ValidationUnverified
	}
	return provenance
}

func confidenceCap(kind core.ClaimKind, hasEvidence bool) float64 {
	switch kind {
	case core.ClaimObservation:
		return 0.6
	case core.ClaimInference:
		return 0.5
	case core.ClaimHypothesis:
		return 0.3
	case core.ClaimInstruction:
		return 0.7
	case core.ClaimVerifiedResult:
		if !hasEvidence {
			return 0.5
		}
		return 0.9
	default:
		return 0.6
	}
}

func validValidation(status core.ValidationStatus) bool {
	switch status {
	case core.ValidationUnverified, core.ValidationSupported, core.ValidationDisputed, core.ValidationSuperseded, core.ValidationVerified:
		return true
	default:
		return false
	}
}

func prepareRecord(record core.MemoryRecord, space core.MemorySpace) core.MemoryRecord {
	if record.Scope == "" {
		record.Scope = space.Scope
	}
	if record.Foreign {
		record.Scope = space.Scope
		confidence := record.ReportedConfidence
		if confidence == 0 {
			confidence = record.Provenance.Confidence
			record.ReportedConfidence = confidence
		}
		record.Provenance = NormalizeProvenance(core.MemoryProvenance{
			ClaimKind: core.ClaimObservation, Confidence: confidence,
			Validation: core.ValidationUnverified,
		})
		return record
	}
	record.Provenance = NormalizeProvenance(record.Provenance)
	return record
}

func uniqueRecords(records []core.MemoryRecord) []core.MemoryRecord {
	byID := make(map[string]core.MemoryRecord, len(records))
	for _, record := range records {
		previous, exists := byID[record.ID]
		if !exists || record.Score > previous.Score {
			byID[record.ID] = record
		}
	}
	unique := make([]core.MemoryRecord, 0, len(byID))
	for _, record := range byID {
		unique = append(unique, record)
	}
	return unique
}

func requestedScopes(scopes []core.MemoryScope) map[core.MemoryScope]bool {
	requested := make(map[core.MemoryScope]bool)
	if len(scopes) == 0 {
		scopes = []core.MemoryScope{core.MemoryScopeAgent, core.MemoryScopeTask, core.MemoryScopeWorkspace}
	}
	for _, scope := range scopes {
		if validScope(scope) {
			requested[scope] = true
		}
	}
	return requested
}

func scopeNames(scopes map[core.MemoryScope]bool) []string {
	ordered := make([]string, 0, len(scopes))
	for scope := range scopes {
		ordered = append(ordered, string(scope))
	}
	sort.Strings(ordered)
	return ordered
}

func evidencePayload(provenance core.MemoryProvenance) []string {
	values := append(append([]string(nil), provenance.EventRefs...), provenance.ArtifactRefs...)
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func errorClass(err error, ctx context.Context) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return "cancelled"
	}
	var classified interface{ ErrorClass() string }
	if errors.As(err, &classified) {
		return classified.ErrorClass()
	}
	return "backend"
}

func validScope(scope core.MemoryScope) bool {
	switch scope {
	case core.MemoryScopeAgent, core.MemoryScopeTask, core.MemoryScopeWorkspace, core.MemoryScopeGlobal:
		return true
	default:
		return false
	}
}

func validClaim(claim core.ClaimKind) bool {
	switch claim {
	case core.ClaimObservation, core.ClaimInference, core.ClaimHypothesis, core.ClaimInstruction, core.ClaimVerifiedResult:
		return true
	default:
		return false
	}
}

func strconvBase10(value uint64) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	i := len(buffer)
	for value > 0 {
		i--
		buffer[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[i:])
}
