package core

import (
	"context"
	"time"
)

// MemoryScope limits which callers may read a memory record.
type MemoryScope string

const (
	MemoryScopeAgent     MemoryScope = "agent"
	MemoryScopeTask      MemoryScope = "task"
	MemoryScopeWorkspace MemoryScope = "workspace"
	MemoryScopeGlobal    MemoryScope = "global"
)

// ClaimKind describes the source and strength of a memory claim.
type ClaimKind string

const (
	ClaimObservation    ClaimKind = "observation"
	ClaimInference      ClaimKind = "inference"
	ClaimHypothesis     ClaimKind = "hypothesis"
	ClaimInstruction    ClaimKind = "instruction"
	ClaimVerifiedResult ClaimKind = "verified_result"
)

// ValidationStatus is separate from a backend's governance state.
type ValidationStatus string

const (
	ValidationUnverified ValidationStatus = "unverified"
	ValidationSupported  ValidationStatus = "supported"
	ValidationDisputed   ValidationStatus = "disputed"
	ValidationSuperseded ValidationStatus = "superseded"
	ValidationVerified   ValidationStatus = "verified"
)

// MemoryProvenance records who made a claim and what evidence supports it.
// MissingEvidence is derived from the evidence and reference fields.
type MemoryProvenance struct {
	ClaimKind       ClaimKind        `json:"claim_kind"`
	Agent           AgentID          `json:"agent,omitempty"`
	Task            TaskID           `json:"task,omitempty"`
	Workspace       string           `json:"workspace,omitempty"`
	EventRefs       []string         `json:"event_refs,omitempty"`
	ArtifactRefs    []string         `json:"artifact_refs,omitempty"`
	Evidence        []string         `json:"evidence,omitempty"`
	Counterevidence []string         `json:"counterevidence,omitempty"`
	Confidence      float64          `json:"confidence"`
	Validation      ValidationStatus `json:"validation"`
	RecordedAt      time.Time        `json:"recorded_at"`
	Challenges      string           `json:"challenges,omitempty"`
	MissingEvidence bool             `json:"missing_evidence"`
}

// MemoryRecord is a backend-neutral memory item.
type MemoryRecord struct {
	ID                 string           `json:"id"`
	Scope              MemoryScope      `json:"scope"`
	Text               string           `json:"text"`
	Subject            string           `json:"subject,omitempty"`
	Provenance         MemoryProvenance `json:"provenance"`
	BackendState       string           `json:"backend_state,omitempty"`
	ReportedConfidence float64          `json:"reported_confidence,omitempty"`
	Score              float64          `json:"score,omitempty"`
	CreatedAt          time.Time        `json:"created_at,omitempty"`
	Foreign            bool             `json:"foreign,omitempty"`
	ConflictIDs        []string         `json:"conflict_ids,omitempty"`
}

// MemoryRecallRequest describes a bounded recall at a safe runtime boundary.
type MemoryRecallRequest struct {
	Query       string        `json:"query"`
	Requester   AgentID       `json:"requester"`
	Task        TaskID        `json:"task,omitempty"`
	ParentTasks []TaskID      `json:"parent_tasks,omitempty"`
	Scopes      []MemoryScope `json:"scopes,omitempty"`
	TopK        int           `json:"top_k,omitempty"`
	Reason      string        `json:"reason,omitempty"`
}

// MemoryRecallResult contains records selected for context and their rendering.
type MemoryRecallResult struct {
	Records          []MemoryRecord `json:"records,omitempty"`
	Context          string         `json:"context,omitempty"`
	Tokens           int            `json:"tokens,omitempty"`
	Filtered         int            `json:"filtered,omitempty"`
	ScopeViolations  int            `json:"scope_violations,omitempty"`
	Conflicts        int            `json:"conflicts,omitempty"`
	Truncated        bool           `json:"truncated,omitempty"`
	TruncatedRecords int            `json:"truncated_records,omitempty"`
}

// MemoryWrite is an immutable candidate for asynchronous storage.
type MemoryWrite struct {
	Scope      MemoryScope      `json:"scope"`
	Text       string           `json:"text"`
	Subject    string           `json:"subject,omitempty"`
	Provenance MemoryProvenance `json:"provenance"`
}

// MemorySpace is an already-resolved backend isolation target.
type MemorySpace struct {
	ID    string      `json:"id"`
	Scope MemoryScope `json:"scope"`
	Agent AgentID     `json:"agent,omitempty"`
	Task  TaskID      `json:"task,omitempty"`
}

// MemoryBackendQuery asks one resolved space for matching memories.
type MemoryBackendQuery struct {
	Space MemorySpace `json:"space"`
	Query string      `json:"query"`
	TopK  int         `json:"top_k"`
}

// MemoryBackendWrite stores one record in one resolved space.
type MemoryBackendWrite struct {
	Space             MemorySpace  `json:"space"`
	Record            MemoryRecord `json:"record"`
	SkipAutoSupersede bool         `json:"skip_auto_supersede"`
}

// MemoryBackend is the small storage contract used by the memory service.
// Implementations must return when the context is cancelled.
type MemoryBackend interface {
	Retrieve(context.Context, MemoryBackendQuery) ([]MemoryRecord, error)
	Observe(context.Context, MemoryBackendWrite) (MemoryRecord, error)
	Get(context.Context, MemorySpace, string) (MemoryRecord, error)
}

// Memory event types are stable names for the workspace event log.
const (
	MemoryEventUnavailable        = "memory.unavailable"
	MemoryEventRetrievalStarted   = "memory.retrieval_started"
	MemoryEventRetrievalCompleted = "memory.retrieval_completed"
	MemoryEventRetrievalFailed    = "memory.retrieval_failed"
	MemoryEventContextInjected    = "memory.context_injected"
	MemoryEventWriteQueued        = "memory.write_queued"
	MemoryEventWriteStored        = "memory.write_stored"
	MemoryEventWriteFailed        = "memory.write_failed"
	MemoryEventWriteDropped       = "memory.write_dropped"
	MemoryEventWriteSkipped       = "memory.write_skipped"
	MemoryEventWriteLost          = "memory.write_lost"
	MemoryEventChallengeRecorded  = "memory.challenge_recorded"
	MemoryEventScopeFiltered      = "memory.scope_filtered"
	MemoryEventSinkDropped        = "memory.sink_events_dropped"
)
