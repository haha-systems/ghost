package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/event"
)

func TestRunEventLogUsesPrivateUniqueFiles(t *testing.T) {
	workdir := t.TempDir()
	legacyDir := filepath.Join(workdir, ".ghost", "events")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(legacyDir, "older.jsonl")
	oldBytes := []byte("leave this file as it is\n")
	if err := os.WriteFile(oldPath, oldBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	first, firstPath, err := openRunEventLog(workdir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, secondPath, err := openRunEventLog(workdir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if firstPath == secondPath || filepath.Dir(firstPath) != legacyDir || filepath.Ext(firstPath) != ".jsonl" {
		t.Fatalf("event log paths = %q and %q", firstPath, secondPath)
	}
	info, err := os.Stat(legacyDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("events directory mode = %o", info.Mode().Perm())
	}
	fileInfo, err := os.Stat(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("event log mode = %o", fileInfo.Mode().Perm())
	}
	got, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(oldBytes) {
		t.Fatal("opening a new run changed the older event file")
	}
}

func TestControllerEventLogFailureIsFailSoft(t *testing.T) {
	workdir := t.TempDir()
	c := New(config.Default(), nil, Options{WorkDir: workdir})
	t.Cleanup(func() { _ = c.Close() })
	if err := c.call(func(o *owner) { _ = o.eventLog.Close() }); err != nil {
		t.Fatal(err)
	}
	if err := c.Record("", event.Event{Source: "GHOST", Kind: event.KindSystem, Message: "keep running"}, nil); err != nil {
		t.Fatalf("event log error stopped the controller: %v", err)
	}
	status := c.Status()
	if status.EventLogError == nil {
		t.Fatal("event log write error was not exposed in status")
	}
	if got := c.Events(EventFilter{Type: "runtime.event"}); len(got) != 1 {
		t.Fatal("event was not kept in the in-memory projection after log failure")
	}
	if core.CategoryOf("runtime.event") != core.CategoryCoordination {
		t.Fatal("test category invalid")
	}
}

func TestControllerAppendsCoreEventsToRunFile(t *testing.T) {
	c := New(config.Default(), nil, Options{WorkDir: t.TempDir()})
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Record("", event.Event{Source: "GHOST", Kind: event.KindSystem, Message: "write once"}, nil); err != nil {
		t.Fatal(err)
	}
	var stored []core.Event
	if err := c.call(func(o *owner) {
		if err := o.eventLog.Replay(func(item core.Event) error {
			stored = append(stored, item)
			return nil
		}); err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 3 || stored[0].Type != "workspace.initialized" || stored[1].Type != "workspace.recovered" || stored[2].Seq != 3 || stored[2].Type != "runtime.event" {
		t.Fatalf("logged core events = %#v", stored)
	}
}

func TestControllerMovesEventLogToSelectedWorkspace(t *testing.T) {
	firstDir, nextDir := t.TempDir(), t.TempDir()
	c := New(config.Default(), nil, Options{WorkDir: firstDir})
	t.Cleanup(func() { _ = c.Close() })
	firstPath := c.Status().EventLogPath
	if err := c.Record("", event.Event{Source: "GHOST", Kind: event.KindSystem, Message: "first workspace"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.SetEventLogWorkDir(nextDir); err != nil {
		t.Fatal(err)
	}
	if err := c.Record("", event.Event{Source: "GHOST", Kind: event.KindSystem, Message: "selected workspace"}, nil); err != nil {
		t.Fatal(err)
	}
	nextPath := c.Status().EventLogPath
	if filepath.Dir(nextPath) != filepath.Join(nextDir, ".ghost", "events") || firstPath == nextPath {
		t.Fatalf("event log paths = %q and %q", firstPath, nextPath)
	}
	firstBytes, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstBytes) == 0 {
		t.Fatal("old workspace log lost its event")
	}
	nextBytes, err := os.ReadFile(nextPath)
	if err != nil {
		t.Fatal(err)
	}
	var item core.Event
	if err := json.Unmarshal(nextBytes, &item); err != nil || item.Type != "runtime.event" || item.Seq != 4 {
		t.Fatalf("selected workspace log event = %#v, err=%v", item, err)
	}
}
