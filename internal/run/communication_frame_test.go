package run

import (
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
)

func TestConsultationRequestFrameTellsConsultantHowToReply(t *testing.T) {
	workspaceID, err := core.NewID("workspace")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := newWorkspaceProjection(core.WorkspaceID(workspaceID), config.AgentSet{"veil": {Runtime: "fake"}}, []string{"veil"})
	if err != nil {
		t.Fatal(err)
	}
	consult := core.Message{ID: "message_1", Sender: core.HumanAgentID, Type: core.MessageRequest, Content: `{"consultation_id":"consultation_1","question":"review add.go"}`}
	if frame := formatMessageFrame(workspace, consult, nil); !strings.Contains(frame, consultationReplyHint) {
		t.Fatalf("consultation frame lacks reply hint:\n%s", frame)
	}
	plain := core.Message{ID: "message_2", Sender: core.HumanAgentID, Type: core.MessageRequest, Content: "please look at add.go"}
	if frame := formatMessageFrame(workspace, plain, nil); strings.Contains(frame, consultationReplyHint) {
		t.Fatalf("ordinary request frame has consultation hint:\n%s", frame)
	}
}

func TestQueuedDeliveryForCancelledTaskIsSkippedButResultsStillFlow(t *testing.T) {
	c, _, _ := newScript(t, []string{})
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("cancel before queued delivery"); err != nil {
		t.Fatal(err)
	}
	tasks := c.WorkspaceSnapshot().Tasks
	if len(tasks) != 1 {
		t.Fatalf("tasks=%#v", tasks)
	}
	taskID := tasks[0].ID
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return c.WorkspaceSnapshot().Tasks[0].Status == core.TaskCancelled
	})
	var delivery *sessionDelivery
	var agent core.AgentID
	_ = c.call(func(o *owner) {
		delivery = o.delivery
		agent, _ = o.workspace.agentID("wraith")
	})
	work := sessionDeliveryJob{agent: agent, message: core.Message{TaskID: &taskID, Type: core.MessageRequest}}
	if !delivery.cancelledWork(work) {
		t.Fatal("queued work for a cancelled task would still be delivered")
	}
	result := sessionDeliveryJob{agent: agent, message: core.Message{TaskID: &taskID, Type: core.MessageTaskResult}}
	if delivery.cancelledWork(result) {
		t.Fatal("cancellation result was skipped")
	}
	unscoped := sessionDeliveryJob{agent: agent, message: core.Message{Type: core.MessageDirect}}
	if delivery.cancelledWork(unscoped) {
		t.Fatal("message without a task was skipped")
	}
}
