package cognition

import (
	"fmt"
	"strings"

	"github.com/haha-systems/ghost/internal/epistemic"
)

// PhaseContract is the whole of an agent's authority for one CES phase. The
// agent is told what this phase is for, what it may do, what belongs to a later
// phase, and the exact JSON object ParsePhaseArtifact accepts. It is never told
// the orchestration algorithm: Ghost owns phase choice, QAC owns resource
// choice, and the agent owns only the artifact.
type PhaseContract struct {
	Phase   epistemic.Phase
	Purpose string
	May     []string
	MustNot []string
	Output  string
}

const contractPreamble = `This phase contract overrides the normal instruction to complete the
entire user task. Completing this phase artifact completes your current
assignment. Ghost will decide what happens next.`

const contractOutputRules = `Return ONLY one JSON object, exactly matching the schema below. No
Markdown, no code fence, no prose or commentary before or after it. Unknown fields are rejected.
Omit any field you have nothing to say about. local_ref values are your
own short labels ("o1", "h1"); they are rewritten into CES identifiers
after the artifact is accepted. Every source and target must be a
local_ref you declared in this artifact or an id present in the
projection. qac_request describes how hard this work is now; it does not
name a resource and it does not name a phase.`

const contractQACSchema = `  "qac_request": {
    "direction": "escalate" | "release",
    "uncertainty": 0.0-1.0,
    "novelty": 0.0-1.0,
    "expected_gain": 0.0-1.0,
    "failed_attempts": 0,
    "reason": "",
    "unresolved": "",
    "evidence": [""],
    "attempted": [""],
    "recommended_focus": ""
  }`

// Contract returns the authority boundary and output schema for a phase.
func Contract(phase epistemic.Phase) (PhaseContract, bool) {
	contract, ok := contracts[phase]
	return contract, ok
}

var contracts = map[epistemic.Phase]PhaseContract{
	epistemic.PhaseTriage: {
		Phase:   epistemic.PhaseTriage,
		Purpose: "Establish what is actually being reported and what is not yet known. You are describing the problem, not solving it.",
		May: []string{
			"Read code, run read-only commands, and inspect evidence.",
			"Record the observations you actually made, so later phases have evidence to reason from.",
			"Record claims, but only when evidence_ref names an observation: one declared in this artifact's observations, or one in the projection. CES rejects an unsupported claim, so omit the claim and record an unknown instead when you cannot cite one.",
			"Record the unknowns that a diagnosis would have to resolve.",
			"Classify the work and name the single next investigation.",
		},
		MustNot: []string{
			"Determine or assert a root cause. That is the ABDUCE phase.",
			"Design, propose, or sketch a fix.",
			"Edit, create, delete, or revert any production file.",
			"Run verification intended to prove a fix.",
			"Claim the task is complete, or continue into any later phase.",
		},
		Output: `{
  "classification": "required: what kind of work this is",
  "boundaries": ["what is in and out of scope"],
  "observations": [{"local_ref": "o1", "content": "what you saw, verbatim where possible"}],
  "claims": [{"local_ref": "c1", "text": "", "evidence_ref": "required: o1, or the id of an observation in the projection"}],
  "unknowns": [{"local_ref": "u1", "question": ""}],
  "resolved_unknowns": [{"local_ref": "r1", "kind": "resolves", "source": "c1", "target": "projection unknown id"}],
  "next_investigation": "required: the one thing to look at next",
` + contractQACSchema + `
}`,
	},
	epistemic.PhaseAbduce: {
		Phase:   epistemic.PhaseAbduce,
		Purpose: "Propose competing mechanisms that would explain the triage evidence, and say which one currently leads.",
		May: []string{
			"Read code and run read-only commands to discriminate between mechanisms.",
			"Propose several hypotheses, each with the observation that would falsify it.",
			"Record supports, contradicts, and resolves relations against projection objects.",
			"Name the leading hypothesis by its local_ref.",
		},
		MustNot: []string{
			"Implement anything, or edit any production file.",
			"Design the fix or the plan of work. That is the FRAME phase.",
			"Assert a hypothesis as confirmed; confirmation happens in CLOSE.",
			"Claim the task is complete, or continue into any later phase.",
		},
		Output: `{
  "hypotheses": [{"local_ref": "h1", "mechanism": "required: how this would produce the symptom", "falsifier": "the observation that would kill it"}],
  "relations": [{"local_ref": "r1", "kind": "supports" | "contradicts" | "derived_from" | "depends_on", "source": "projection id or local_ref", "target": "h1"}],
  "leading_hypothesis_ref": "required: h1",
  "remaining_uncertainty": "",
  "resolved_unknowns": [{"local_ref": "r2", "kind": "resolves", "source": "h1", "target": "projection unknown id"}],
` + contractQACSchema + `
}`,
	},
	epistemic.PhaseFrame: {
		Phase:   epistemic.PhaseFrame,
		Purpose: "Turn the leading hypothesis into one frame: what doing this work means, when it is done, and what would show the frame is wrong.",
		May: []string{
			"Read code and run read-only commands to size the work.",
			"State the completion conditions the work must satisfy.",
			"State the disconfirmation conditions that would invalidate this frame.",
			"Record constraints the execution must respect.",
			"Supersede an earlier frame by naming its projection id.",
		},
		MustNot: []string{
			"Edit, create, delete, or revert any production file. Execution is the EXECUTE phase.",
			"Run a build or test intended to verify a change you made.",
			"Claim the task is complete, or continue into any later phase.",
		},
		Output: `{
  "frame": {
    "local_ref": "required: f1",
    "name": "required: short name",
    "summary": "required: what doing this work means",
    "completion_conditions": [""],
    "disconfirmation_conditions": [""]
  },
  "constraints": [{"local_ref": "k1", "text": ""}],
  "relations": [{"local_ref": "r1", "kind": "implements" | "depends_on" | "derived_from", "source": "f1", "target": "projection hypothesis id"}],
  "supersedes_frame_ref": "projection id of a frame this replaces, if any",
` + contractQACSchema + `
}`,
	},
	epistemic.PhaseExecute: {
		Phase:   epistemic.PhaseExecute,
		Purpose: "Carry out the work the active frame describes, and record what each action actually produced.",
		May: []string{
			"Edit files and run commands in service of the active frame.",
			"Record each action taken, the outcome it produced, and the observations behind them.",
			"Record a contradicts relation against the frame, the leading hypothesis, an action, or an outcome when the evidence goes against it.",
		},
		MustNot: []string{
			"Work outside the active frame, or silently reframe the task. If the frame is wrong, record the contradicting evidence and stop.",
			"Replace the frame or propose a new hypothesis; surface the contradiction instead and let Ghost route.",
			"Declare the change verified, or claim the task is complete. Verification is the CLOSE phase.",
		},
		Output: `{
  "observations": [{"local_ref": "o1", "content": "what you saw while doing the work"}],
  "actions": [{"local_ref": "a1", "description": "what you did"}],
  "outcomes": [{"local_ref": "x1", "description": "what it produced"}],
  "observation_refs": ["projection observation ids you relied on"],
  "relations": [{"local_ref": "r1", "kind": "supports" | "contradicts" | "implements" | "derived_from", "source": "a1", "target": "x1 or a projection id"}],
  "resolved_unknowns": [{"local_ref": "r2", "kind": "resolves", "source": "x1", "target": "projection unknown id"}],
` + contractQACSchema + `
}`,
	},
	epistemic.PhaseClose: {
		Phase:   epistemic.PhaseClose,
		Purpose: "Try to falsify the work. Check the frame's completion conditions against reality and report what you found.",
		May: []string{
			"Run tests, builds, and read-only checks that could disconfirm the work, and record what they produced as observations.",
			"Record supports or contradicts relations between the verification evidence and the leading hypothesis, frame, actions, or outcomes.",
			"State the residual uncertainty honestly, including what you did not check.",
			"Recommend completion. Ghost decides whether work completes; your recommendation is evidence, not a verdict.",
		},
		MustNot: []string{
			"Repair anything you find broken. Record the contradicting evidence instead; Ghost will route the repair.",
			"Suppress, soften, or omit a failure in order to close.",
			"Set the work status, or declare the task complete on your own authority.",
		},
		Output: `{
  "observations": [{"local_ref": "o1", "content": "what the verification produced"}],
  "verification_observation_refs": ["projection observation ids that carry the verification evidence"],
  "relations": [{"local_ref": "r1", "kind": "supports" | "contradicts" | "tests", "source": "projection observation id", "target": "projection hypothesis, frame, action, or outcome id"}],
  "residual_uncertainty": "what remains unchecked or unexplained",
  "completion_recommended": true,
` + contractQACSchema + `
}`,
	},
}

func (c PhaseContract) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "CES PHASE: %s\n\n%s\n\nPURPOSE\n%s\n\nYOU MAY\n%s\n\nYOU MUST NOT\n%s\n\nOUTPUT\n%s\n\n%s\n",
		strings.ToUpper(string(c.Phase)), contractPreamble, c.Purpose, bullets(c.May), bullets(c.MustNot), contractOutputRules, c.Output)
	return b.String()
}

func bullets(items []string) string {
	if len(items) == 0 {
		return "- nothing"
	}
	return "- " + strings.Join(items, "\n- ")
}
