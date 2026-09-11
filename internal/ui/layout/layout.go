// Package layout owns the geometry shared by Ghost's screens.
//
// Both screens previously computed their own chrome budget from duplicated
// magic numbers, which let the steering editor claim a fixed twenty rows and
// starve the log beneath it. Centralising the arithmetic keeps the two screens
// in step and makes the row budget testable on its own.
package layout

// Terminal and pane constraints.
const (
	MinWidth  = 80
	MinHeight = 24

	// SidebarWidth is the fixed meta column on the left of every screen.
	SidebarWidth = 30
	// GutterWidth separates the sidebar from the content column.
	GutterWidth = 3

	// FramePadX and FramePadY mirror theme.Frame's padding.
	FramePadX = 2
	FramePadY = 1

	// MaxSteeringRows caps how far the steering editor may grow.
	MaxSteeringRows = 20
	// MinSteeringRows keeps a single visible line when the editor is empty.
	MinSteeringRows = 1
	// MinPaneRows is the smallest useful height for a scrolling pane.
	MinPaneRows = 3
)

// Screen is the resolved geometry for one render pass.
type Screen struct {
	Width, Height int
	// TooSmall reports that the terminal cannot host the layout.
	TooSmall bool
	// Sidebar and Content are column widths; Sidebar is zero when the terminal
	// is too narrow to afford one.
	Sidebar, Content int
	// Panes holds the body height of each scrolling pane, top to bottom.
	Panes []int
	// Steering is the body height of the steering editor.
	Steering int
}

// Compute resolves the geometry for a screen with len(weights) scrolling panes
// stacked above a steering editor.
//
// steeringRows is the editor's desired height (its current line count); it is
// clamped to [MinSteeringRows, MaxSteeringRows] and further reduced so every
// pane keeps at least MinPaneRows. weights distribute the remaining rows and
// need not sum to one. helpRows reserves space for an expanded help footer.
func Compute(width, height, steeringRows int, weights []float64, helpRows int) Screen {
	s := Screen{Width: width, Height: height}
	// An unsized screen has nothing to budget; callers must render the guidance
	// block rather than index into empty panes.
	if width < MinWidth || height < MinHeight {
		s.TooSmall = true
		return s
	}
	if len(weights) == 0 {
		weights = []float64{1}
	}

	inner := width - 2*FramePadX
	if inner < 1 {
		inner = 1
	}
	if inner >= SidebarWidth+GutterWidth+40 {
		s.Sidebar = SidebarWidth
		s.Content = inner - SidebarWidth - GutterWidth
	} else {
		s.Content = inner
	}

	rows := height - 2*FramePadY - helpRows
	// Each pane carries a one-row title, and a one-row rule separates every
	// pane from the next as well as the steering editor from the panes.
	chrome := 2*len(weights) + 1
	body := rows - chrome
	minBody := MinPaneRows*len(weights) + MinSteeringRows
	if body < minBody {
		s.TooSmall = true
		return s
	}

	s.Steering = clamp(steeringRows, MinSteeringRows, MaxSteeringRows)
	if room := body - MinPaneRows*len(weights); s.Steering > room {
		s.Steering = room
	}
	s.Panes = distribute(body-s.Steering, weights)
	return s
}

// distribute splits total across weights, giving every pane at least
// MinPaneRows and handing any rounding remainder to the largest pane.
func distribute(total int, weights []float64) []int {
	sum := 0.0
	for _, w := range weights {
		sum += w
	}
	out := make([]int, len(weights))
	assigned := 0
	widest, widestWeight := 0, -1.0
	for i, w := range weights {
		n := MinPaneRows
		if sum > 0 {
			n = int(float64(total) * w / sum)
		}
		if n < MinPaneRows {
			n = MinPaneRows
		}
		out[i] = n
		assigned += n
		if w > widestWeight {
			widest, widestWeight = i, w
		}
	}
	// Reconcile against the true total so the panes always fill the column.
	for assigned > total {
		shrunk := false
		for i := range out {
			if assigned == total {
				break
			}
			if out[i] > MinPaneRows {
				out[i]--
				assigned--
				shrunk = true
			}
		}
		if !shrunk {
			break
		}
	}
	if assigned < total {
		out[widest] += total - assigned
	}
	return out
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// SteeringRows reports how many rows an editor holding value wants, counting
// wrapped continuations against the given width.
func SteeringRows(value string, width int) int {
	if value == "" {
		return MinSteeringRows
	}
	if width < 1 {
		width = 1
	}
	rows, line := 0, 0
	for _, r := range value {
		if r == '\n' {
			rows += wrapped(line, width)
			line = 0
			continue
		}
		line++
	}
	rows += wrapped(line, width)
	return clamp(rows, MinSteeringRows, MaxSteeringRows)
}

func wrapped(cells, width int) int {
	if cells <= 0 {
		return 1
	}
	n := cells / width
	if cells%width != 0 {
		n++
	}
	return n
}
