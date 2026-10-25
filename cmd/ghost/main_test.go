package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUsageDescribesConversationKeys(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-h"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Tab/Shift+Tab  cycle focus",
		"0-9            select ALL (0) or an agent tab",
		"Ctrl+E         toggle the execution events panel",
		":ces on|off",
		"Ctrl+X cancels the run",
		":memory",
		":recall QUERY",
		":remember TEXT",
		":legacy",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("usage missing %q", want)
		}
	}
}
