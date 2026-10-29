package core

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestReadOnlyReplayToleratesTruncatedTailWithoutChangingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(validEvent()); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"broken":`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var replayed []Event
	report, err := ReadOnlyReplay(path, func(item Event) error {
		replayed = append(replayed, item)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SkippedTailLines != 1 || len(replayed) != 1 || replayed[0].ID != validEvent().ID {
		t.Fatalf("report=%#v replayed=%#v", report, replayed)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("read-only replay changed the old event file")
	}
}

func TestCreateRefusesExistingLogWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	want := []byte("old bytes stay as written\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(path); !errors.Is(err, os.ErrExist) {
		t.Fatalf("Create error = %v, want file-exists error", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("existing file changed: got %q", got)
	}
}

func TestIDGeneratorSkipsReservedIDs(t *testing.T) {
	clock := time.Unix(0, 100)
	generator := NewIDGenerator(func() time.Time { return clock }, bytes.NewReader(bytes.Repeat([]byte{0x2a}, 32)))
	reserved := "event_0000000000000064" + "2a2a2a2a2a2a2a2a"
	if err := generator.Reserve(reserved); err != nil {
		t.Fatal(err)
	}
	got, err := generator.New("event")
	if err != nil {
		t.Fatal(err)
	}
	if got == reserved {
		t.Fatalf("generator reused reserved ID %q", got)
	}
}
