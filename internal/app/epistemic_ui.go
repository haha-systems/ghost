package app

import (
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
	epistemicui "github.com/haha-systems/ghost/internal/ui/epistemic"
)

// EpistemicUpdateMsg is the app boundary for CES read state and semantic events.
type EpistemicUpdateMsg struct {
	View      epistemic.OperatorView
	Cognition string
	Snapshot  epistemicui.Snapshot
	Events    []event.Event
}

// SetEpistemicView applies the app-facing CES projection and the separate
// resource name to both read-only UI surfaces.
func (m Model) SetEpistemicView(view epistemic.OperatorView, cognition string, snapshot epistemicui.Snapshot) Model {
	snapshot.Operator = view
	m.dashboard = m.dashboard.SetOperatorView(&view, cognition).SetOperatorSupplement(snapshot.Supplement)
	m.epistemic = m.epistemic.SetSnapshot(snapshot).SetSize(m.width, m.height)
	return m
}
