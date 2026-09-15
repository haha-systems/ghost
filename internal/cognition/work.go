package cognition

import (
	"time"

	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/qac"
)

type WorkState = epistemic.WorkStatus

const (
	WorkActive     WorkState = epistemic.WorkActive
	WorkComplete   WorkState = epistemic.WorkComplete
	WorkIncomplete WorkState = epistemic.WorkIncomplete
)

type WorkItem struct {
	ID, Goal                  string
	Importance                float64
	OwnerResource, OwnerAgent string
	State                     WorkState
	TerminalReason            string
	Handoffs                  int
	CreatedAt, UpdatedAt      time.Time
}
type Plan struct {
	ID, From, To, Goal string
	WorkID             string
	Initial            bool
	Action             qac.Action
	Decision           qac.Decision
	Request            QACRequest
	Visible            string
}
