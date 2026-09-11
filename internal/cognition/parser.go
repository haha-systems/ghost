package cognition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

var qacBlock = regexp.MustCompile(`(?s)<QAC_REQUEST>\s*(.*?)\s*</QAC_REQUEST>`)

func ParseQACRequest(text string) (QACRequest, string, bool, error) {
	matches := qacBlock.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return QACRequest{}, text, false, nil
	}
	if len(matches) != 1 {
		return QACRequest{}, text, false, fmt.Errorf("qac request: expected one block")
	}
	m := matches[0]
	body := text[m[2]:m[3]]
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return QACRequest{}, text, true, fmt.Errorf("qac request: %w", err)
	}
	if _, ok := raw["importance"]; ok {
		return QACRequest{}, text, true, fmt.Errorf("qac request: importance is not allowed")
	}
	var r QACRequest
	d := json.NewDecoder(bytes.NewReader([]byte(body)))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return QACRequest{}, text, true, fmt.Errorf("qac request: %w", err)
	}
	if r.Direction != "escalate" && r.Direction != "release" {
		return QACRequest{}, text, true, fmt.Errorf("qac request: invalid direction %q", r.Direction)
	}
	for _, v := range []float64{r.Uncertainty, r.Novelty, r.ExpectedGain} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return QACRequest{}, text, true, fmt.Errorf("qac request: signal must be in [0,1]")
		}
	}
	if r.FailedAttempts < 0 {
		return QACRequest{}, text, true, fmt.Errorf("qac request: failed_attempts must be non-negative")
	}
	return r, strings.TrimSpace(text[:m[0]] + text[m[1]:]), true, nil
}
