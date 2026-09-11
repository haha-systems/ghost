package cognition

type QACRequest struct {
	Direction        string   `json:"direction"`
	Uncertainty      float64  `json:"uncertainty"`
	Novelty          float64  `json:"novelty"`
	ExpectedGain     float64  `json:"expected_gain"`
	FailedAttempts   int      `json:"failed_attempts"`
	Reason           string   `json:"reason"`
	Unresolved       string   `json:"unresolved"`
	Evidence         []string `json:"evidence"`
	Attempted        []string `json:"attempted"`
	RecommendedFocus string   `json:"recommended_focus"`
}
