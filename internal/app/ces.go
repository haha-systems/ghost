package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/epistemic"
	uiEpistemic "github.com/haha-systems/ghost/internal/ui/epistemic"
)

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
	assign("H", objectIDs(state.Hypotheses, func(value epistemic.Hypothesis) epistemic.ID { return value.ID }))
	assign("U", objectIDs(state.Unknowns, func(value epistemic.Unknown) epistemic.ID { return value.ID }))
	assign("F", objectIDs(state.Frames, func(value epistemic.Frame) epistemic.ID { return value.ID }))
	assign("C", objectIDs(state.Claims, func(value epistemic.Claim) epistemic.ID { return value.ID }))
	assign("O", objectIDs(state.Observations, func(value epistemic.Observation) epistemic.ID { return value.ID }))
	assign("K", objectIDs(state.Constraints, func(value epistemic.Constraint) epistemic.ID { return value.ID }))
	assign("A", objectIDs(state.Actions, func(value epistemic.Action) epistemic.ID { return value.ID }))
	assign("X", objectIDs(state.Outcomes, func(value epistemic.Outcome) epistemic.ID { return value.ID }))
	assign("R", objectIDs(state.Relations, func(value epistemic.Relation) epistemic.ID { return value.ID }))
	return aliases
}

func objectIDs[T any](values []T, id func(T) epistemic.ID) []epistemic.ID {
	ids := make([]epistemic.ID, 0, len(values))
	for _, value := range values {
		ids = append(ids, id(value))
	}
	return ids
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
