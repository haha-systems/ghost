package cognition

import (
	"fmt"
	"github.com/haha-systems/qac"
	"strings"
)

func BuildHandoff(work WorkItem, request QACRequest, decision qac.Decision) string {
	return fmt.Sprintf("[QAC HANDOFF]\n\nWORK ITEM\n%s\n\nGOAL\n%s\n\nFROM\n%s\n\nTO\n%s\n\nQAC DECISION\n%s.\nScore %.2f; threshold %.2f.\n\nESTABLISHED\n%s\n\nUNRESOLVED\n%s\n\nATTEMPTED\n%s\n\nRECOMMENDED FOCUS\n%s\n\nContinue from this frontier.", work.ID, work.Goal, decision.From, decision.To, decision.Action, decision.Score, decision.Threshold, lines(request.Evidence), request.Unresolved, lines(request.Attempted), request.RecommendedFocus)
}
func lines(items []string) string {
	if len(items) == 0 {
		return "- none"
	}
	return "- " + strings.Join(items, "\n- ")
}
