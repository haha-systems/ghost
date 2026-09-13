package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUsageDescribesAgentScreenKeys(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-h"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Tab cycles agents, steering, and events", "Ctrl+X interrupts a turn", "Esc clears a steering draft before leaving"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("usage missing %q", want)
		}
	}
}
