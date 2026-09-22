package fake

import (
	"sync"
	"testing"

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
