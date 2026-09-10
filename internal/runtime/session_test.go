package runtime

import "testing"

func TestGuardSerializesTurnAndInterruptsOnlyMatchingTurn(t *testing.T) {
	g := NewGuard()
	g.Ready()
	if err := g.StartTurn("t1"); err != nil {
		t.Fatal(err)
	}
	if err := g.StartTurn("t2"); err != ErrTurnActive {
		t.Fatalf("want active error, got %v", err)
	}
	if err := g.BeginInterrupt("t2"); err != ErrNoActiveTurn {
		t.Fatalf("want no-active error, got %v", err)
	}
	if err := g.BeginInterrupt("t1"); err != nil {
		t.Fatal(err)
	}
	if !g.Complete("t1", false) || g.State() != StateIdle {
		t.Fatalf("turn did not return idle: %s", g.State())
	}
}
