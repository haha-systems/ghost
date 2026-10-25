package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/core"
)

func TestReadWorkspaceEventHistoryHandlesEmptyTailAndMiddleCorruption(t *testing.T) {
	workdir := t.TempDir()
	dir := filepath.Join(workdir, ".ghost", "events")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if history, err := readWorkspaceEventHistory(workdir, RecoveryLimits{}); err != nil || len(history.Events) != 0 {
		t.Fatalf("empty history = %#v, err=%v", history, err)
	}

	tailPath := writeHistoryLog(t, dir, "a.jsonl", historyEvent(1))
	file, err := os.OpenFile(tailPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"truncated":`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	tailBefore, err := os.ReadFile(tailPath)
	if err != nil {
		t.Fatal(err)
	}

	middlePath := writeHistoryLog(t, dir, "b.jsonl", historyEvent(2), historyEvent(3))
	middleBefore, err := os.ReadFile(middlePath)
	if err != nil {
		t.Fatal(err)
	}
	lineEnd := bytes.IndexByte(middleBefore, '\n') + 1
	corrupt := append(append([]byte(nil), middleBefore[:lineEnd]...), []byte("not-json\n")...)
	corrupt = append(corrupt, middleBefore[lineEnd:]...)
	if err := os.WriteFile(middlePath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	middleBefore = corrupt

	history, err := readWorkspaceEventHistory(workdir, RecoveryLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Events) != 1 || history.Events[0].ID != historyEvent(1).ID {
		t.Fatalf("replayed events = %#v", history.Events)
	}
	if history.Summary.FilesRead != 2 || history.Summary.FilesSkipped != 1 || history.Summary.SkippedTailLines != 1 {
		t.Fatalf("summary = %#v", history.Summary)
	}
	if len(history.Warnings) < 2 {
		t.Fatalf("warnings = %v", history.Warnings)
	}
	if !containsEventID(history.ReservedEventIDs, historyEvent(3).ID) {
		t.Fatal("valid event after the corrupt line was not reserved")
	}
	if got := history.LastSequences[historyEvent(3).StreamKey()]; got != 2 {
		t.Fatalf("sequence after skipped corrupt file = %d, want 2", got)
	}
	tailAfter, err := os.ReadFile(tailPath)
	if err != nil {
		t.Fatal(err)
	}
	middleAfter, err := os.ReadFile(middlePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(tailAfter, tailBefore) || !bytes.Equal(middleAfter, middleBefore) {
		t.Fatal("history replay changed an old file")
	}
}

func containsEventID(ids []core.EventID, want core.EventID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestReadWorkspaceEventHistoryCapsFilesAndEvents(t *testing.T) {
	workdir := t.TempDir()
	dir := filepath.Join(workdir, ".ghost", "events")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		writeHistoryLog(t, dir, fmt.Sprintf("%d.jsonl", i), historyEvent(i))
	}
	history, err := readWorkspaceEventHistory(workdir, RecoveryLimits{MaxFiles: 2, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Events) != 1 || history.Events[0].ID != historyEvent(3).ID {
		t.Fatalf("capped events = %#v", history.Events)
	}
	if history.Summary.FilesRead != 3 || history.Summary.FilesOmitted != 1 || history.Summary.EventsOmitted != 2 {
		t.Fatalf("cap summary = %#v", history.Summary)
	}
}

func writeHistoryLog(t *testing.T, dir, name string, events ...core.Event) string {
	t.Helper()
	path := filepath.Join(dir, name)
	log, err := core.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range events {
		if _, err := log.Append(item); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func historyEvent(n int) core.Event {
	return core.Event{
		ID: core.EventID(fmt.Sprintf("event_%032x", n)), SchemaVersion: core.EventSchemaVersion,
		Time: time.Unix(int64(n), 0).UTC(), Workspace: core.WorkspaceID("workspace_00000000000000010000000000000001"),
		Actor: core.Actor{Kind: core.ActorSystem, ID: "test"}, Source: "test", Type: "task.created",
		Category: core.CategoryTask, Payload: json.RawMessage(`{}`),
	}
}
