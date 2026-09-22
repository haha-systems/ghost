package cognition

import (
	"testing"
	"time"
)

func TestSnapshotDoesNotMarkOwnerAvailableWithoutSession(t *testing.T) {
	c, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.StartWork("x")
	if err := c.Commit(p, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := c.Snapshot(nil, time.Now()).Resources["wraith"].Available; got {
		t.Fatal("owner was marked available without a usable session")
	}
}
