//go:build live

package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/codex"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/runtime"
)

func TestLiveWraithVeilWraithReviewCycle(t *testing.T) {
	if os.Getenv("GHOST_LIVE") != "1" {
		t.Skip("set GHOST_LIVE=1 to run the live Codex review cycle")
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "add.go"), []byte("package sample\n\nfunc Add(a, b int) int { return a - b }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	model := strings.TrimSpace(os.Getenv("GHOST_LIVE_CODEX_MODEL"))
	effort := strings.TrimSpace(os.Getenv("GHOST_LIVE_CODEX_EFFORT"))
	if effort == "" {
		effort = "low"
	}
	agents := config.AgentSet{
		"wraith": {Runtime: "codex", Model: model, Effort: effort, WorkingDir: workdir, Sandbox: "read_only", GhostMode: true},
		"veil":   {Runtime: "codex", Model: model, Effort: effort, WorkingDir: workdir, Sandbox: "read_only", GhostMode: true},
	}
	cfg := config.Default()
	cfg.Agents = &agents
	cfg.AgentOrder = []string{"wraith", "veil"}
	cfg.Memory.Enabled = false
	cfg.Decisions.AutoConsult = false
	for name, backend := range agents {
		sessionConfig, err := MakeSessionConfig(cfg.Global.InitialPrompt, name, backend)
		if err != nil {
			t.Fatal(err)
		}
		if sessionConfig.Sandbox != runtime.SandboxReadOnly || sessionConfig.Approvals != runtime.ApprovalNever {
			t.Fatalf("%s session permissions = sandbox %v, approvals %v", name, sessionConfig.Sandbox, sessionConfig.Approvals)
		}
	}

	c := New(cfg, nil, Options{Runtime: codex.NewRuntime(), WorkDir: workdir})
	var watchers sync.WaitGroup
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close controller: %v", err)
		}
		watchers.Wait()
	})
	if err := c.SetCESPolicy(CESPolicyOff); err != nil {
		t.Fatal(err)
	}
	if err := c.SetDelegationEnabled(true); err != nil {
		t.Fatal(err)
	}

	sessions, failures := c.StartSessions()
	if len(failures) != 0 {
		t.Fatalf("start Codex sessions: %v", failures)
	}
	for _, name := range []string{"wraith", "veil"} {
		if sessions[name] == nil {
			t.Fatalf("Codex session %q did not start", name)
		}
	}
	for _, session := range sessions {
		watchers.Add(1)
		go func(session runtime.Session) {
			defer watchers.Done()
			for event := range session.Events() {
				_ = c.Observe(event)
			}
		}(session)
	}

	sendDone := make(chan error, 1)
	go func() {
		sendDone <- c.Send([]string{"wraith"}, "Ask VEIL (using a ghost-message or ghost-consult block, whichever this workspace documents for asking another agent for a review) to review add.go for bugs in one sentence. When VEIL replies, reply to me with one sentence summarising VEIL's review. Do not edit files or run commands.")
	}()
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("send review request: %v; %s", err, liveReviewTimeoutDump(c))
		}
	case <-ctx.Done():
		t.Fatalf("timed out sending review request: %v; %s", ctx.Err(), liveReviewTimeoutDump(c))
	}

	workspace := c.WorkspaceSnapshot()
	wraith, veil := liveReviewAgentIDs(workspace.Agents)
	if wraith == "" || veil == "" {
		t.Fatalf("could not find WRAITH and VEIL; %s", liveReviewTimeoutDump(c))
	}
	var rootTask core.TaskID
	for _, message := range workspace.Messages {
		if message.Sender == core.HumanAgentID && containsAgent(message.Recipients, wraith) && message.TaskID != nil {
			rootTask = *message.TaskID
			break
		}
	}
	if rootTask == "" {
		t.Fatalf("human review task is missing; %s", liveReviewTimeoutDump(c))
	}

	workspace, err := waitForLiveReviewCycle(ctx, c, rootTask, wraith, veil, sessions)
	if err != nil {
		t.Fatalf("review cycle did not finish: %v; %s", err, liveReviewTimeoutDump(c))
	}
	for _, message := range workspace.Messages {
		if strings.Contains(message.Content, "item/started") {
			t.Errorf("message contains provider item start metadata: %#v", message)
		}
	}
	for _, event := range workspace.Events {
		if event.Type == "message.rejected" || event.Type == "coordination.routing_rejected" {
			t.Errorf("routing rejection event: %s %s", event.Type, event.Payload)
		}
	}

	turns := map[string]int{
		"wraith": sessions["wraith"].Stats().CompletedTurns,
		"veil":   sessions["veil"].Stats().CompletedTurns,
	}
	var finalMessages []string
	for _, message := range workspace.Messages {
		if message.Sender != wraith && message.Sender != veil {
			continue
		}
		sender := "WRAITH"
		if message.Sender == veil {
			sender = "VEIL"
		}
		recipients := make([]string, 0, len(message.Recipients))
		for _, recipient := range message.Recipients {
			switch recipient {
			case wraith:
				recipients = append(recipients, "WRAITH")
			case veil:
				recipients = append(recipients, "VEIL")
			case core.HumanAgentID:
				recipients = append(recipients, "you")
			default:
				recipients = append(recipients, string(recipient))
			}
		}
		finalMessages = append(finalMessages, fmt.Sprintf("%s -> %s: %s", sender, strings.Join(recipients, ","), message.Content))
	}
	t.Logf("elapsed=%s turns per agent=%v final messages=%q", time.Since(started), turns, finalMessages)
}

func liveReviewAgentIDs(agents []core.Agent) (core.AgentID, core.AgentID) {
	var wraith, veil core.AgentID
	for _, agent := range agents {
		switch strings.ToLower(agent.DisplayName) {
		case "wraith":
			wraith = agent.ID
		case "veil":
			veil = agent.ID
		}
	}
	return wraith, veil
}

func waitForLiveReviewCycle(ctx context.Context, c *Controller, taskID core.TaskID, wraith, veil core.AgentID, sessions map[string]runtime.Session) (WorkspaceSnapshot, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		workspace := c.WorkspaceSnapshot()
		taskIndex, reviewIndex, finalIndex := -1, -1, -1
		for i, message := range workspace.Messages {
			if taskIndex == -1 && message.Sender == wraith && containsAgent(message.Recipients, veil) {
				taskIndex = i
			}
			if taskIndex >= 0 && reviewIndex == -1 && i > taskIndex && message.Sender == veil && containsAgent(message.Recipients, wraith) {
				reviewIndex = i
			}
			if reviewIndex >= 0 && finalIndex == -1 && i > reviewIndex && message.Sender == wraith && containsAgent(message.Recipients, core.HumanAgentID) {
				finalIndex = i
			}
		}
		if taskIndex >= 0 && reviewIndex > taskIndex && finalIndex > reviewIndex && !c.Status().Active {
			for _, task := range workspace.Tasks {
				if task.ID == taskID && task.Status.IsTerminal() {
					wraithState, veilState := sessions["wraith"].State(), sessions["veil"].State()
					if wraithState == runtime.StateFailed || veilState == runtime.StateFailed {
						return workspace, fmt.Errorf("agent session failed: WRAITH=%s VEIL=%s", wraithState, veilState)
					}
					if wraithState == runtime.StateIdle && veilState == runtime.StateIdle {
						return workspace, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return workspace, ctx.Err()
		case <-ticker.C:
		}
	}
}

func liveReviewTimeoutDump(c *Controller) string {
	workspace := c.WorkspaceSnapshot()
	start := len(workspace.Events) - 30
	if start < 0 {
		start = 0
	}
	return fmt.Sprintf("all messages=%#v recent events=%#v", workspace.Messages, workspace.Events[start:])
}
