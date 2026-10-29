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

func TestBloodwireGlobalBindings(t *testing.T) {
	keys := Default()
	bindings := []struct {
		name string
		msg  tea.KeyPressMsg
		key  key.Binding
	}{
		{"palette", tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModCtrl}), keys.OpenPalette},
		{"events", tea.KeyPressMsg(tea.Key{Code: 'e', Mod: tea.ModCtrl}), keys.ToggleEvents},
		{"cancel", tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}), keys.Cancel},
		{"CES", tea.KeyPressMsg(tea.Key{Code: 'c', Text: "c"}), keys.ToggleCES},
		{"follow", tea.KeyPressMsg(tea.Key{Code: 'f', Text: "f"}), keys.ToggleFollow},
		{"event category", tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"}), keys.CycleCategory},
		{"agent filter", tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}), keys.ToggleAgent},
	}
	for _, tc := range bindings {
		t.Run(tc.name, func(t *testing.T) {
			if !key.Matches(tc.msg, tc.key) {
				t.Fatalf("%s binding did not match", tc.name)
			}
		})
	}
}
