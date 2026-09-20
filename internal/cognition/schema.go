package cognition

import (
	"github.com/haha-systems/ghost/internal/epistemic"
)

func PhaseOutputSchema(phase epistemic.Phase, projection epistemic.Projection) map[string]any {
	stringValue := map[string]any{"type": "string"}
	stringArray := map[string]any{"type": "array", "items": stringValue}
	relation := objectSchema([]string{"local_ref", "kind", "source", "target"}, map[string]any{
		"local_ref": stringValue,
		"kind":      map[string]any{"type": "string"},
		"source":    stringValue,
		"target":    stringValue,
	})
	if phase == epistemic.PhaseAbduce {
		sources := make([]any, 0, len(projection.State.Observations)+len(projection.State.Claims))
		for _, value := range projection.State.Observations {
			sources = append(sources, string(value.ID))
		}
		for _, value := range projection.State.Claims {
			sources = append(sources, string(value.ID))
		}
		relation["properties"].(map[string]any)["kind"] = map[string]any{"type": "string", "enum": []any{"supports", "contradicts"}}
		if len(sources) > 0 {
			relation["properties"].(map[string]any)["source"] = map[string]any{"type": "string", "enum": sources}
		}
	}
	relations := map[string]any{"type": "array", "items": relation}
	qac := objectSchema(
		[]string{"direction", "uncertainty", "novelty", "expected_gain", "failed_attempts", "reason", "unresolved", "evidence", "attempted", "recommended_focus"},
		map[string]any{
			"direction":         map[string]any{"type": "string"},
			"uncertainty":       map[string]any{"type": "number"},
			"novelty":           map[string]any{"type": "number"},
			"expected_gain":     map[string]any{"type": "number"},
			"failed_attempts":   map[string]any{"type": "integer"},
			"reason":            stringValue,
			"unresolved":        stringValue,
			"evidence":          stringArray,
			"attempted":         stringArray,
			"recommended_focus": stringValue,
		},
	)
	observation := objectSchema([]string{"local_ref", "content"}, map[string]any{"local_ref": stringValue, "content": stringValue})
	claim := objectSchema([]string{"local_ref", "text", "evidence_ref"}, map[string]any{"local_ref": stringValue, "text": stringValue, "evidence_ref": stringValue})
	unknown := objectSchema([]string{"local_ref", "question"}, map[string]any{"local_ref": stringValue, "question": stringValue})
	hypothesis := objectSchema([]string{"local_ref", "mechanism", "falsifier"}, map[string]any{"local_ref": stringValue, "mechanism": stringValue, "falsifier": stringValue})
	constraint := objectSchema([]string{"local_ref", "text"}, map[string]any{"local_ref": stringValue, "text": stringValue})
	action := objectSchema([]string{"local_ref", "description"}, map[string]any{"local_ref": stringValue, "description": stringValue})
	outcome := objectSchema([]string{"local_ref", "description"}, map[string]any{"local_ref": stringValue, "description": stringValue})
	frame := objectSchema([]string{"local_ref", "name", "summary", "completion_conditions", "disconfirmation_conditions"}, map[string]any{
		"local_ref": stringValue, "name": stringValue, "summary": stringValue,
		"completion_conditions": stringArray, "disconfirmation_conditions": stringArray,
	})
	array := func(items map[string]any) map[string]any { return map[string]any{"type": "array", "items": items} }

	switch phase {
	case epistemic.PhaseTriage:
		return objectSchema([]string{"classification", "boundaries", "observations", "claims", "unknowns", "resolved_unknowns", "next_investigation", "qac_request"}, map[string]any{
			"classification": stringValue, "boundaries": stringArray, "observations": array(observation), "claims": array(claim),
			"unknowns": array(unknown), "resolved_unknowns": relations, "next_investigation": stringValue, "qac_request": qac,
		})
	case epistemic.PhaseAbduce:
		return objectSchema([]string{"hypotheses", "relations", "leading_hypothesis_ref", "remaining_uncertainty", "qac_request"}, map[string]any{
			"hypotheses": array(hypothesis), "relations": relations, "leading_hypothesis_ref": stringValue,
			"remaining_uncertainty": stringValue, "qac_request": qac,
		})
	case epistemic.PhaseFrame:
		return objectSchema([]string{"frame", "constraints", "relations", "supersedes_frame_ref", "qac_request"}, map[string]any{
			"frame": frame, "constraints": array(constraint), "relations": relations, "supersedes_frame_ref": stringValue, "qac_request": qac,
		})
	case epistemic.PhaseExecute:
		return objectSchema([]string{"observations", "actions", "outcomes", "relations", "qac_request"}, map[string]any{
			"observations": array(observation), "actions": array(action), "outcomes": array(outcome), "relations": relations, "qac_request": qac,
		})
	case epistemic.PhaseClose:
		return objectSchema([]string{"observations", "relations", "resolved_unknowns", "residual_uncertainty", "completion_recommended", "qac_request"}, map[string]any{
			"observations": array(observation), "relations": relations, "resolved_unknowns": relations,
			"residual_uncertainty": stringValue, "completion_recommended": map[string]any{"type": "boolean"}, "qac_request": qac,
		})
	default:
		return nil
	}
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
