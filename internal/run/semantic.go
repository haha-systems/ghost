package run

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
)

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

// cesAliases gives every object the short, stable label the operator reads.
// CES identifiers are opaque; these are display names, not identity.
func cesAlias(state epistemic.State, id epistemic.ID) (string, string) {
	alias := cesAliases(state)[id]
	for _, v := range state.Hypotheses {
		if v.ID == id {
			return alias, v.Mechanism
		}
	}
	for _, v := range state.Unknowns {
		if v.ID == id {
			return alias, v.Question
		}
	}
	for _, v := range state.Frames {
		if v.ID == id {
			return alias, v.Summary
		}
	}
	for _, v := range state.Claims {
		if v.ID == id {
			return alias, v.Text
		}
	}
	for _, v := range state.Observations {
		if v.ID == id {
			return alias, v.Content
		}
	}
	for _, v := range state.Constraints {
		if v.ID == id {
			return alias, v.Text
		}
	}
	for _, v := range state.Actions {
		if v.ID == id {
			return alias, v.Description
		}
	}
	for _, v := range state.Outcomes {
		if v.ID == id {
			return alias, v.Description
		}
	}
	return alias, ""
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
