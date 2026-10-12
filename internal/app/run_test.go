package app

import (
	tea "charm.land/bubbletea/v2"
	"sync"
	"testing"
	"time"
)

var appTestRuntimes sync.Map

type phaseStepMsg struct{}

func phaseStepCmd(m Model) tea.Cmd { return func() tea.Msg { return phaseStepMsg{} } }
func drainRunBoundary(t *testing.T, m Model) Model {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case e := <-m.runEvents:
			updated, _ := m.handleRunEvent(e)
			m = updated.(Model)
			if e.Boundary {
				return m
			}
		case <-timer.C:
			t.Fatal("controller event timeout")
			return m
		}
	}
}
