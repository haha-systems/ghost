package keymap

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func TestEpistemicInspectorHasCentralEKey(t *testing.T) {
	msg := tea.KeyPressMsg(tea.Key{Code: 'e', Text: "e"})
	if !key.Matches(msg, Default().OpenEpistemic) {
		t.Fatal("E is not bound to the epistemic inspector")
	}
}
