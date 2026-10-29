package fake

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/runtime"
)

func TestEmitAndCloseAreRaceSafe(t *testing.T) {
	for i := 0; i < 100; i++ {
		s := NewSession("race")
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			for j := 0; j < 100; j++ {
				s.Emit(runtime.Event{Kind: runtime.KindMessage})
			}
		}()
		go func() {
			defer group.Done()
			_ = s.Close()
		}()
		group.Wait()
	}
}

func TestScriptedSendEmitsResponderEventsAndCompletesTurn(t *testing.T) {
	s := NewSession("wraith")
	s.OnSend = func(input runtime.Input) []runtime.Event {
		if input.Text != "inspect" {
			t.Fatalf("responder input = %q", input.Text)
		}
		return []runtime.Event{
			{Kind: runtime.KindMessage, Summary: "findings", Metadata: map[string]string{"backend_method": "item/agentMessage/completed"}},
			{Kind: runtime.KindStatus, Metadata: map[string]string{"backend_method": "turn/completed"}},
		}
	}
	if err := s.Send(context.Background(), runtime.Input{Text: "inspect"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case item := <-s.Events():
			if item.AgentID != "wraith" || item.SessionID != "wraith" || item.TurnID != "fake-turn" {
				t.Fatalf("responder event identity = %#v", item)
			}
		case <-time.After(time.Second):
			t.Fatal("scripted response event timed out")
		}
	}
	if got := s.State(); got != runtime.StateIdle {
		t.Fatalf("state after scripted completion = %s", got)
	}
}

func TestScriptedSteerEmitsResponderEvents(t *testing.T) {
	s := NewSession("veil")
	if err := s.Send(context.Background(), runtime.Input{Text: "start"}); err != nil {
		t.Fatal(err)
	}
	s.OnSteer = func(runtime.Input) []runtime.Event {
		return []runtime.Event{{Kind: runtime.KindStatus, Metadata: map[string]string{"backend_method": "turn/completed"}}}
	}
	if err := s.Steer(context.Background(), runtime.Input{Text: "continue"}); err != nil {
		t.Fatal(err)
	}
	select {
	case item := <-s.Events():
		if item.TurnID != "fake-turn" || item.Metadata["backend_method"] != "turn/completed" {
			t.Fatalf("steer response event = %#v", item)
		}
	case <-time.After(time.Second):
		t.Fatal("scripted steer event timed out")
	}
	if got := s.State(); got != runtime.StateIdle {
		t.Fatalf("state after steered completion = %s", got)
	}
}
