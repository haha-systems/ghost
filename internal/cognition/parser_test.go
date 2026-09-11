package cognition

import (
	"github.com/haha-systems/qac"
	"strings"
	"testing"
)

const validRequest = "answer\n<QAC_REQUEST>\n{\"direction\":\"escalate\",\"uncertainty\":0.84,\"novelty\":0.62,\"expected_gain\":0.88,\"failed_attempts\":2,\"reason\":\"race\",\"unresolved\":\"turn state\",\"evidence\":[\"test\"],\"attempted\":[\"writer\"],\"recommended_focus\":\"derive race\"}\n</QAC_REQUEST>"

func TestParseQACRequest(t *testing.T) {
	got, visible, found, err := ParseQACRequest(validRequest)
	if err != nil || !found {
		t.Fatalf("found=%t err=%v", found, err)
	}
	if got.Direction != "escalate" || got.FailedAttempts != 2 || strings.Contains(visible, "QAC_REQUEST") {
		t.Fatalf("request=%#v visible=%q", got, visible)
	}
}
func TestParseQACRequestRejectsInvalidBlocks(t *testing.T) {
	for _, tt := range []struct{ name, text string }{{"bad json", "<QAC_REQUEST>{</QAC_REQUEST>"}, {"two", validRequest + validRequest}, {"importance", strings.Replace(validRequest, "\"direction\"", "\"importance\":0.9,\"direction\"", 1)}, {"range", strings.Replace(validRequest, "0.84", "1.2", 1)}} {
		t.Run(tt.name, func(t *testing.T) {
			_, visible, _, err := ParseQACRequest(tt.text)
			if err == nil || visible != tt.text {
				t.Fatalf("visible=%q err=%v", visible, err)
			}
		})
	}
}
func TestBuildHandoffKeepsFrontier(t *testing.T) {
	text := BuildHandoff(WorkItem{ID: "work-1", Goal: "fix race"}, QACRequest{Evidence: []string{"test"}, Unresolved: "turn state", Attempted: []string{"writer"}, RecommendedFocus: "derive race"}, qac.Decision{Action: qac.ActionEscalate, From: "shade", To: "veil", Score: .84, Threshold: .80})
	for _, want := range []string{"fix race", "shade", "veil", "test", "turn state", "writer", "derive race"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
}
