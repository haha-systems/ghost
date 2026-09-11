package cognition

import "time"

import "github.com/haha-systems/qac"

type WorkState string

const (
	WorkActive WorkState = "active"
	WorkPaused WorkState = "paused"
	WorkDone   WorkState = "done"
)

type WorkItem struct {
	ID, Goal                  string
	Importance                float64
	OwnerResource, OwnerAgent string
	State                     WorkState
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
}
