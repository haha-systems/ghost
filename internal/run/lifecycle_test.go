package run

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/comms"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

type manualRunClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *manualRunClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *manualRunClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTimedBrokerController(t *testing.T) (*Controller, map[string]*brokerTestSession, *manualRunClock, chan time.Time) {
	t.Helper()
	clock := &manualRunClock{now: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	ticks := make(chan time.Time, 1)
	agents := config.AgentSet{
		"wraith": {Runtime: "fake"},
		"veil":   {Runtime: "fake"},
		"shade":  {Runtime: "fake"},
	}
	c := New(config.Config{Agents: &agents, AgentOrder: []string{"wraith", "veil", "shade"}}, nil, Options{
		Clock: clock, TickChannel: ticks,
	})
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	sessions := map[string]*brokerTestSession{}
	installed := map[string]runtime.Session{}
	for _, name := range []string{"wraith", "veil", "shade"} {
		session := &brokerTestSession{Session: fake.NewSession(name)}
		sessions[name] = session
		installed[name] = session
	}
	if err := c.SetSessions(installed); err != nil {
		t.Fatal(err)
	}
	watchBrokerSessions(t, c, sessions)
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c, sessions, clock, ticks
}

func triggerBrokerTick(t *testing.T, clock *manualRunClock, ticks chan time.Time, advance time.Duration) {
	t.Helper()
	clock.Advance(advance)
	ticks <- clock.Now()
}

func controllerConsultations(t *testing.T, c *Controller) []comms.Consultation {
	t.Helper()
	var consultations []comms.Consultation
	if err := c.call(func(o *owner) {
		if o.router != nil {
			consultations = o.router.Snapshot().Consultations
		}
	}); err != nil {
		t.Fatal(err)
	}
	return consultations
}

func TestBrokerTickExpiresQuietChildAndCompletesParent(t *testing.T) {
	c, sessions, clock, ticks := newTimedBrokerController(t)
	var parentSends atomic.Int32
	sessions["wraith"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		send := parentSends.Add(1)
		if send == 2 {
			return channelEvent("wraith", "The child timed out; I will finish the review.")
		}
		if send != 1 {
			return nil
		}
		parent := frameValue(input.Text, "task_id")
		block := "```ghost-delegate\n" + `{"schema_version":1,"target":"veil","parent_task_id":"` + parent + `","objective":"Review the handler","context_refs":[],"expected_deliverable":"Findings","timeout":"1s"}` + "\n```"
		return channelEvent("wraith", block)
	}
	if err := c.Send([]string{"wraith"}, "Review the handler"); err != nil {
		t.Fatal(err)
	}
	waitForBroker(t, c, func() bool {
		workspace := c.WorkspaceSnapshot()
		if len(workspace.Tasks) != 2 {
			return false
		}
		for _, message := range workspace.Messages {
			if message.Type == core.MessageTaskDelegation && message.Delivery == core.DeliveryDelivered {
				return true
			}
		}
		return false
	})
	triggerBrokerTick(t, clock, ticks, 2*time.Second)
	waitForBroker(t, c, func() bool {
		workspace := c.WorkspaceSnapshot()
		return len(workspace.Tasks) == 2 && workspace.Tasks[0].Status == core.TaskCompleted &&
			workspace.Tasks[1].Status == core.TaskFailed && !c.Status().Active
	})
	workspace := c.WorkspaceSnapshot()
	if workspace.Tasks[1].FailureReason != "timeout" {
		t.Fatalf("child failure reason = %q", workspace.Tasks[1].FailureReason)
	}
}

func TestBrokerTickExpiresConsultationAndCompletesParent(t *testing.T) {
	c, sessions, clock, ticks := newTimedBrokerController(t)
	configureConsultingParent(t, sessions)
	if err := c.Send([]string{"wraith"}, "Review the event boundary"); err != nil {
		t.Fatal(err)
	}
	waitForBroker(t, c, func() bool {
		consultations := controllerConsultations(t, c)
		return len(consultations) == 1 && consultations[0].Status == comms.ConsultationOpen &&
			consultationRequestDelivered(c, consultations[0])
	})
	triggerBrokerTick(t, clock, ticks, 11*time.Minute)
	waitForBroker(t, c, func() bool {
		consultations := controllerConsultations(t, c)
		workspace := c.WorkspaceSnapshot()
		return len(consultations) == 1 && consultations[0].Status == comms.ConsultationTimedOut &&
			len(workspace.Tasks) == 1 && workspace.Tasks[0].Status == core.TaskCompleted && !c.Status().Active
	})
}

func TestControllerCloseTicksExpiredConsultation(t *testing.T) {
	c, sessions, clock, _ := newTimedBrokerController(t)
	configureConsultingParent(t, sessions)
	if err := c.Send([]string{"wraith"}, "Review the event boundary"); err != nil {
		t.Fatal(err)
	}
	waitForBroker(t, c, func() bool {
		consultations := controllerConsultations(t, c)
		return len(consultations) == 1 && consultations[0].Status == comms.ConsultationOpen &&
			consultationRequestDelivered(c, consultations[0])
	})
	var current *owner
	if err := c.call(func(o *owner) { current = o }); err != nil {
		t.Fatal(err)
	}
	clock.Advance(11 * time.Minute)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	consultations := current.router.Snapshot().Consultations
	workspace := current.workspace.snapshot()
	if len(consultations) != 1 || consultations[0].Status != comms.ConsultationTimedOut ||
		len(workspace.Tasks) != 1 || workspace.Tasks[0].Status != core.TaskCompleted {
		t.Fatalf("shutdown tick state: consultations=%#v tasks=%#v", consultations, workspace.Tasks)
	}
}

func TestConsultantReplyResolvesConsultationAndReturnsResult(t *testing.T) {
	c, sessions, _, _ := newTimedBrokerController(t)
	configureConsultingParent(t, sessions)
	sessions["shade"].Session.OnSend = func(runtime.Input) []runtime.Event {
		return channelEvent("shade", "The boundary is safe; keep the caller identity.")
	}
	if err := c.Send([]string{"wraith"}, "Review the event boundary"); err != nil {
		t.Fatal(err)
	}
	waitForBroker(t, c, func() bool {
		consultations := controllerConsultations(t, c)
		if len(consultations) != 1 || consultations[0].Status != comms.ConsultationCompleted ||
			consultations[0].Result == nil || !strings.Contains(consultations[0].Result.Findings, "caller identity") {
			return false
		}
		for _, message := range c.WorkspaceSnapshot().Messages {
			if message.Type == core.MessageReply && message.Sender == consultations[0].Target &&
				message.ReplyTo != nil && *message.ReplyTo == consultations[0].RequestMessageID &&
				containsAgent(message.Recipients, consultations[0].Requester) && strings.Contains(message.Content, "caller identity") {
				return true
			}
		}
		return false
	})
	for _, message := range c.WorkspaceSnapshot().Messages {
		if message.Type == core.MessageReply && strings.Contains(message.Content, "caller identity") {
			assertConsultationMessageResult(t, message)
		}
	}
	waitForBroker(t, c, func() bool {
		for _, input := range sessions["wraith"].capturedInputs() {
			if strings.Contains(input.Text, "caller identity") {
				return true
			}
		}
		return false
	})
	waitForBroker(t, c, func() bool {
		workspace := c.WorkspaceSnapshot()
		return len(workspace.Tasks) == 1 && workspace.Tasks[0].Status == core.TaskCompleted && !c.Status().Active
	})
}

func consultationRequestDelivered(c *Controller, consultation comms.Consultation) bool {
	for _, message := range c.WorkspaceSnapshot().Messages {
		if message.ID == consultation.RequestMessageID {
			return message.RecipientDelivery[consultation.Target] == core.DeliveryDelivered
		}
	}
	return false
}

func TestConsultantTurnFailureFailsConsultation(t *testing.T) {
	c, sessions, _, _ := newTimedBrokerController(t)
	configureConsultingParent(t, sessions)
	sessions["shade"].Session.OnSend = func(runtime.Input) []runtime.Event {
		return []runtime.Event{{
			AgentID: "shade", Kind: runtime.KindStatus, Summary: "provider turn failed",
			Metadata: map[string]string{"backend_method": "turn/failed"},
		}}
	}
	if err := c.Send([]string{"wraith"}, "Review the event boundary"); err != nil {
		t.Fatal(err)
	}
	waitForBroker(t, c, func() bool {
		consultations := controllerConsultations(t, c)
		workspace := c.WorkspaceSnapshot()
		return len(consultations) == 1 && consultations[0].Status == comms.ConsultationFailed &&
			consultations[0].FailureReason == "provider turn failed" && len(workspace.Tasks) == 1 &&
			workspace.Tasks[0].Status == core.TaskCompleted
	})
}

func configureConsultingParent(t *testing.T, sessions map[string]*brokerTestSession) {
	t.Helper()
	var sends atomic.Int32
	sessions["wraith"].Session.OnSend = func(input runtime.Input) []runtime.Event {
		if sends.Add(1) != 1 {
			return nil
		}
		parent := frameValue(input.Text, "task_id")
		block := "```ghost-consult\n" + `{"schema_version":1,"target":"shade","parent_task_id":"` + parent + `","question":"Review the event boundary","evidence":["event order"]}` + "\n```"
		return channelEvent("wraith", block)
	}
}

func assertConsultationMessageResult(t *testing.T, message core.Message) {
	t.Helper()
	var result comms.ConsultationResult
	if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
		t.Fatal(err)
	}
	if result.Findings == "" || result.Uncertainty == "" {
		t.Fatalf("consultation result message = %#v", result)
	}
}
