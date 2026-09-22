package cognition

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
	"github.com/haha-systems/qac"
)

type scriptedJev struct {
	raw   []byte
	err   error
	calls int
	input JevInput
}

func (j *scriptedJev) Assess(_ context.Context, input JevInput) ([]byte, error) {
	j.calls++
	j.input = input
	return j.raw, j.err
}

type blockingJev struct{}

func (blockingJev) Assess(ctx context.Context, _ JevInput) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func activeCoordinator(t *testing.T) *Coordinator {
	t.Helper()
	c, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	start, err := c.StartWork("Ghostdive integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Commit(start, time.Now()); err != nil {
		t.Fatal(err)
	}
	return c
}

func lowExecuteSituation() CognitiveSituation {
	return CognitiveSituation{
		Phase:            epistemic.PhaseExecute,
		RepeatedAttempts: 3,
		ArtifactAccepted: true,
		Progress: ProgressEvidence{
			State:      ProgressLow,
			Provenance: ProgressStructuredExecution,
			Strength:   1,
		},
		HasLeadingHypothesis: true,
		HasActiveFrame:       true,
	}
}

func availableResources() map[string]runtime.Session {
	return map[string]runtime.Session{
		"wraith": fake.NewSession("wraith"),
		"shade":  fake.NewSession("shade"),
		"veil":   fake.NewSession("veil"),
	}
}

func TestRepeatedExecuteWithVerifiedLowProgressRequestsJev(t *testing.T) {
	c := activeCoordinator(t)
	jev := &scriptedJev{raw: []byte(`{"current_resource_adequacy":0.1,"expected_gain_from_stronger_cognition":0.9,"decomposition_need":0.2,"confidence":0.9,"judgment":"escalate_cognition"}`)}
	c.SetJev(jev)
	plan, err := c.AllocateWithSituation(context.Background(), QACRequest{Uncertainty: .1, ExpectedGain: .1}, lowExecuteSituation(), availableResources(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if jev.calls != 1 || plan.Jev == nil || !plan.Jev.Used {
		t.Fatalf("jev calls=%d assessment=%#v", jev.calls, plan.Jev)
	}
	if got := jev.input.ProgressState; got != ProgressLow {
		t.Fatalf("progress = %q, want low", got)
	}
}

func TestUnknownProgressDoesNotRequestJevOrImplyEscalation(t *testing.T) {
	c := activeCoordinator(t)
	jev := &scriptedJev{raw: []byte(`{"current_resource_adequacy":0,"expected_gain_from_stronger_cognition":1,"decomposition_need":0,"confidence":1,"judgment":"escalate_cognition"}`)}
	c.SetJev(jev)
	situation := lowExecuteSituation()
	situation.Progress = ProgressEvidence{State: ProgressUnknown, Provenance: ProgressMissing}
	plan, err := c.AllocateWithSituation(context.Background(), QACRequest{}, situation, availableResources(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if jev.calls != 0 || plan.Action != qac.ActionContinue {
		t.Fatalf("calls=%d plan=%#v", jev.calls, plan)
	}
}

func TestJevAdviceCanBeRejectedByBudget(t *testing.T) {
	c := activeCoordinator(t)
	c.bindings["shade"] = binding{id: "shade", agent: "shade", capability: .65, cost: .3, scarcity: .3, max: intp(0)}
	c.SetJev(&scriptedJev{raw: []byte(`{"current_resource_adequacy":0,"expected_gain_from_stronger_cognition":1,"decomposition_need":0,"confidence":1,"judgment":"escalate_cognition"}`)})
	plan, err := c.AllocateWithSituation(context.Background(), QACRequest{}, lowExecuteSituation(), availableResources(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action == qac.ActionEscalate {
		t.Fatalf("budget did not constrain advisory escalation: %#v", plan)
	}
}

func TestDecompositionAdviceDoesNotBecomeEscalation(t *testing.T) {
	c := activeCoordinator(t)
	c.SetJev(&scriptedJev{raw: []byte(`{"current_resource_adequacy":0.1,"expected_gain_from_stronger_cognition":1,"decomposition_need":1,"confidence":1,"judgment":"reframe_or_decompose"}`)})
	plan, err := c.AllocateWithSituation(context.Background(), QACRequest{}, lowExecuteSituation(), availableResources(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != qac.ActionContinue {
		t.Fatalf("decomposition advice escalated: %#v", plan)
	}
}

func TestJevContinuationKeepsCurrentResource(t *testing.T) {
	c := activeCoordinator(t)
	c.SetJev(&scriptedJev{raw: []byte(`{"current_resource_adequacy":0.9,"expected_gain_from_stronger_cognition":0.1,"decomposition_need":0,"confidence":1,"judgment":"continue_current_resource"}`)})
	plan, err := c.AllocateWithSituation(context.Background(), QACRequest{Uncertainty: .1, ExpectedGain: .1}, lowExecuteSituation(), availableResources(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != qac.ActionContinue || plan.To != "wraith" {
		t.Fatalf("plan=%#v", plan)
	}
}

func TestMalformedOrUnavailableJevFallsBack(t *testing.T) {
	for _, jev := range []*scriptedJev{
		{raw: []byte(`{"resource":"veil"}`)},
		{err: errors.New("unavailable")},
		{raw: []byte(`{"current_resource_adequacy":0.5,"expected_gain_from_stronger_cognition":0.5,"decomposition_need":0.5,"confidence":0.5,"judgment":"insufficient_evidence"}`)},
	} {
		c := activeCoordinator(t)
		c.SetJev(jev)
		plan, err := c.AllocateWithSituation(context.Background(), QACRequest{}, lowExecuteSituation(), availableResources(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if plan.Jev == nil || plan.Jev.Used || plan.Action != qac.ActionContinue {
			t.Fatalf("plan=%#v", plan)
		}
	}
}

func TestTimedOutJevFallsBack(t *testing.T) {
	c := activeCoordinator(t)
	c.SetJev(blockingJev{})
	c.SetJevTimeout(time.Millisecond)
	plan, err := c.AllocateWithSituation(context.Background(), QACRequest{}, lowExecuteSituation(), availableResources(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Jev == nil || plan.Jev.IgnoreReason != "unavailable" || plan.Action != qac.ActionContinue {
		t.Fatalf("plan=%#v", plan)
	}
}

func TestJevJudgmentRejectsResourceSelection(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"current_resource_adequacy":0.5,"expected_gain_from_stronger_cognition":0.5,"decomposition_need":0.5,"confidence":0.5,"judgment":"escalate_cognition","resource":"VEIL"}`),
		[]byte(`{"current_resource_adequacy":0.5,"expected_gain_from_stronger_cognition":0.5,"decomposition_need":0.5,"confidence":0.5,"judgment":"escalate_cognition","model":"gpt"}`),
		[]byte(`{"current_resource_adequacy":0.5,"expected_gain_from_stronger_cognition":0.5,"decomposition_need":0.5,"confidence":0.5}`),
	} {
		if _, err := ParseJevJudgment(raw); err == nil {
			t.Fatalf("judgment was accepted: %s", raw)
		}
	}
}

func TestFixedSituationAndJevResultAllocateDeterministically(t *testing.T) {
	raw := []byte(`{"current_resource_adequacy":0.1,"expected_gain_from_stronger_cognition":0.9,"decomposition_need":0.2,"confidence":0.9,"judgment":"escalate_cognition"}`)
	var plans []Plan
	for range 2 {
		c := activeCoordinator(t)
		c.SetJev(&scriptedJev{raw: raw})
		plan, err := c.AllocateWithSituation(context.Background(), QACRequest{}, lowExecuteSituation(), availableResources(), time.Unix(1, 0))
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, plan)
	}
	if plans[0].Action != plans[1].Action || plans[0].To != plans[1].To || plans[0].Request.ExpectedGain != plans[1].Request.ExpectedGain || plans[0].Request.Uncertainty != plans[1].Request.Uncertainty {
		t.Fatalf("plans differ: %#v %#v", plans[0], plans[1])
	}
}
