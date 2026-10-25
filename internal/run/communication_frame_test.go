package run

import (
	"strings"
	"testing"

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
