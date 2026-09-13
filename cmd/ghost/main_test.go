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
	for _, want := range []string{
		"Tab/Shift+Tab  cycle focus",
		"End resumes live follow",
		"Ctrl+X interrupts a running turn",
		"clear a steering draft",
		"scrolls whichever pane the pointer is over",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("usage missing %q", want)
		}
	}
}
