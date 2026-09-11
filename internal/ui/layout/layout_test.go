package layout

import "testing"

func TestComputeRejectsSmallTerminals(t *testing.T) {
	for _, c := range []struct{ w, h int }{{79, 40}, {120, 23}} {
		if got := Compute(c.w, c.h, 1, []float64{1}, 0); !got.TooSmall {
			t.Fatalf("%dx%d: expected TooSmall", c.w, c.h)
		}
	}
}

func TestComputeFillsTheColumn(t *testing.T) {
	for _, panes := range [][]float64{{1}, {0.4, 0.6}} {
		for _, h := range []int{24, 32, 40, 60} {
			s := Compute(120, h, 1, panes, 0)
			if s.TooSmall {
				t.Fatalf("panes=%d h=%d: unexpectedly too small", len(panes), h)
			}
			total := s.Steering
			for _, p := range s.Panes {
				if p < MinPaneRows {
					t.Fatalf("panes=%d h=%d: pane %d below minimum", len(panes), h, p)
				}
				total += p
			}
			want := h - 2*FramePadY - (2*len(panes) + 1)
			if total != want {
				t.Fatalf("panes=%d h=%d: rows %d, want %d", len(panes), h, total, want)
			}
		}
	}
}

// The log pane starving to two rows was the original defect: an idle steering
// editor must never claim the column just because it may grow to twenty rows.
func TestIdleSteeringDoesNotStarveTheLog(t *testing.T) {
	s := Compute(120, 32, 1, []float64{1}, 0)
	if s.Steering != MinSteeringRows {
		t.Fatalf("idle steering = %d rows, want %d", s.Steering, MinSteeringRows)
	}
	if s.Panes[0] < 20 {
		t.Fatalf("log pane = %d rows, want the remaining column", s.Panes[0])
	}
}

func TestSteeringGrowsButIsCapped(t *testing.T) {
	grown := Compute(120, 60, 12, []float64{1}, 0)
	if grown.Steering != 12 {
		t.Fatalf("steering = %d, want 12", grown.Steering)
	}
	capped := Compute(120, 60, 500, []float64{1}, 0)
	if capped.Steering != MaxSteeringRows {
		t.Fatalf("steering = %d, want cap %d", capped.Steering, MaxSteeringRows)
	}
}

func TestSteeringYieldsToPaneMinimums(t *testing.T) {
	s := Compute(120, 24, 20, []float64{0.4, 0.6}, 0)
	if s.TooSmall {
		t.Fatal("80x24 with two panes should still lay out")
	}
	for _, p := range s.Panes {
		if p < MinPaneRows {
			t.Fatalf("pane %d below minimum while steering took %d", p, s.Steering)
		}
	}
}

func TestColumnsFillTheFrameWidth(t *testing.T) {
	for _, w := range []int{80, 100, 120, 200} {
		s := Compute(w, 40, 1, []float64{1}, 0)
		inner := w - 2*FramePadX
		used := s.Content
		if s.Sidebar > 0 {
			used += s.Sidebar + GutterWidth
		}
		if used != inner {
			t.Fatalf("w=%d: columns total %d, want inner %d", w, used, inner)
		}
		if s.Content < 1 {
			t.Fatalf("w=%d: content column collapsed", w)
		}
	}
}

func TestSteeringRowsCountsWrapping(t *testing.T) {
	cases := []struct {
		value string
		width int
		want  int
	}{
		{"", 40, 1},
		{"one line", 40, 1},
		{"a\nb\nc", 40, 3},
		{"trailing\n", 40, 2},
		{"aaaaaaaaaa", 4, 3},
	}
	for _, c := range cases {
		if got := SteeringRows(c.value, c.width); got != c.want {
			t.Fatalf("SteeringRows(%q, %d) = %d, want %d", c.value, c.width, got, c.want)
		}
	}
}
