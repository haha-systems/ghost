package ghostdive

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/memory"
)

type observeRequest struct {
	Layer             string        `json:"layer"`
	Subject           string        `json:"subject,omitempty"`
	Summary           string        `json:"summary,omitempty"`
	Confidence        float64       `json:"confidence"`
	AgentID           string        `json:"agent_id,omitempty"`
	Provenance        provenanceDTO `json:"provenance,omitempty"`
	EvidenceRefs      []string      `json:"evidence_refs"`
	SkipAutoSupersede bool          `json:"skip_auto_supersede"`
}

type retrieveRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
	Mode  string `json:"mode"`
}

type provenanceDTO struct {
	SourceKind     string `json:"source_kind,omitempty"`
	SourceRef      string `json:"source_ref,omitempty"`
	SourceRecordID string `json:"source_record_id,omitempty"`
	Note           string `json:"note,omitempty"`
}

type recordDTO struct {
	ID             string        `json:"id"`
	Layer          string        `json:"layer"`
	PromotionState string        `json:"promotion_state"`
	Subject        string        `json:"subject"`
	Predicate      string        `json:"predicate"`
	Object         string        `json:"object"`
	Summary        string        `json:"summary"`
	Confidence     float64       `json:"confidence"`
	AgentID        string        `json:"agent_id"`
	Provenance     provenanceDTO `json:"provenance"`
	EvidenceRefs   []string      `json:"evidence_refs"`
	Sequence       int64         `json:"sequence"`
	CreatedAt      string        `json:"created_at"`
	UpdatedAt      string        `json:"updated_at"`
}

type retrieveHit struct {
	recordDTO
	Score float64 `json:"score"`
}

type retrieveResponse struct {
	Hits []retrieveHit `json:"hits"`
}

type envelope struct {
	Version         int                   `json:"v"`
	Scope           core.MemoryScope      `json:"scope,omitempty"`
	Agent           core.AgentID          `json:"agent,omitempty"`
	Task            core.TaskID           `json:"task,omitempty"`
	Workspace       string                `json:"workspace,omitempty"`
	Claim           core.ClaimKind        `json:"claim,omitempty"`
	Validation      core.ValidationStatus `json:"validation,omitempty"`
	Evidence        []string              `json:"evidence,omitempty"`
	Counterevidence []string              `json:"counterevidence,omitempty"`
	Events          []string              `json:"events,omitempty"`
	Artifacts       []string              `json:"artifacts,omitempty"`
	RecordedAt      time.Time             `json:"recorded_at,omitempty"`
}

func decodeRecord(dto recordDTO, space core.MemorySpace, score float64) core.MemoryRecord {
	text := dto.Summary
	if text == "" {
		text = dto.Object
	}
	if text == "" {
		text = dto.Predicate
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, dto.CreatedAt)
	record := core.MemoryRecord{
		ID: dto.ID, Scope: space.Scope, Text: text, Subject: dto.Subject,
		BackendState: dto.PromotionState, ReportedConfidence: dto.Confidence,
		Score: score, CreatedAt: createdAt,
	}
	if provenance, ok := decodeProvenance(dto.Provenance, dto.Confidence, dto.EvidenceRefs); ok {
		record.Scope = provenanceScope(dto.Provenance, space.Scope)
		record.Provenance = memory.NormalizeProvenance(provenance)
		return record
	}
	record.Foreign = true
	record.Provenance = memory.NormalizeProvenance(core.MemoryProvenance{
		ClaimKind: core.ClaimObservation, Confidence: dto.Confidence,
		Validation: core.ValidationUnverified,
	})
	return record
}

func decodeProvenance(dto provenanceDTO, confidence float64, evidenceRefs []string) (core.MemoryProvenance, bool) {
	if !strings.HasPrefix(dto.SourceKind, "ghost/") || dto.Note == "" {
		return core.MemoryProvenance{}, false
	}
	var value envelope
	if err := json.Unmarshal([]byte(dto.Note), &value); err != nil || value.Version != 1 || !validMemoryScope(value.Scope) || !validClaim(value.Claim) || dto.SourceKind != "ghost/"+string(value.Claim) {
		return core.MemoryProvenance{}, false
	}
	owner := string(value.Agent)
	if owner == "" {
		owner = string(value.Task)
	}
	expectedRef := fmt.Sprintf("ghost://%s/%s/%s", value.Workspace, value.Scope, owner)
	if dto.SourceRef != "" && dto.SourceRef != expectedRef {
		return core.MemoryProvenance{}, false
	}
	evidence := copyStrings(value.Evidence)
	eventRefs := copyStrings(value.Events)
	artifactRefs := copyStrings(value.Artifacts)
	knownRefs := referenceSet(evidence, eventRefs, artifactRefs)
	known := make(map[string]bool, len(knownRefs))
	for _, ref := range knownRefs {
		known[ref] = true
	}
	for _, ref := range evidenceRefs {
		if ref != "" && !known[ref] {
			evidence = append(evidence, ref)
			known[ref] = true
		}
	}
	return core.MemoryProvenance{
		ClaimKind: value.Claim, Agent: value.Agent, Task: value.Task, Workspace: value.Workspace,
		EventRefs: eventRefs, ArtifactRefs: artifactRefs, Evidence: evidence,
		Counterevidence: copyStrings(value.Counterevidence), Confidence: confidence,
		Validation: value.Validation, RecordedAt: value.RecordedAt, Challenges: dto.SourceRecordID,
	}, true
}

func provenanceScope(dto provenanceDTO, fallback core.MemoryScope) core.MemoryScope {
	if dto.Note == "" {
		return fallback
	}
	var value struct {
		Scope core.MemoryScope `json:"scope"`
	}
	if json.Unmarshal([]byte(dto.Note), &value) == nil && validMemoryScope(value.Scope) {
		return value.Scope
	}
	return fallback
}

func validMemoryScope(scope core.MemoryScope) bool {
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
