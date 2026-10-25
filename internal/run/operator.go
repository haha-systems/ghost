package run

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/event"
)

// Approval is one operator decision about a paused action.
type Approval struct {
	ID            string
	Actor         string
	ActionKind    string
	Command       []string
	Reason        string
	Deadline      time.Time
	Decision      string
	Responder     string
	Note          string
	ResolveReason string
}

type approvalRecord struct {
	Approval
	resolved bool
}

// Override queues a one-phase resource choice. It is applied at the next
// allocation boundary and then cleared.
func (c *Controller) Override(resource, reason string) error {
	var err error
	callErr := c.call(func(o *owner) {
		if o.stopping {
			err = ErrClosed
			return
		}
		if o.cognition == nil {
			err = errors.New("cognition is not configured")
			return
		}
		err = o.cognition.SetOverride(resource, reason)
		if err == nil {
			o.record("", event.Event{Source: "QAC", Kind: event.KindQAC, Message: "override queued for " + strings.ToUpper(resource), Metadata: map[string]string{"resource": resource, "reason": reason}}, nil)
		}
		o.publishBoundary()
	})
	return errors.Join(err, callErr)
}

// ClearOverride drops a queued resource choice before the next allocation.
func (c *Controller) ClearOverride() error {
	return c.call(func(o *owner) {
		if o.cognition != nil {
			o.cognition.ClearOverride()
		}
		o.record("", event.Event{Source: "QAC", Kind: event.KindQAC, Message: "override cleared"}, nil)
		o.publishBoundary()
	})
}

// RequestApproval pauses an action until RespondApproval or the deadline.
func (c *Controller) RequestApproval(actor, actionKind, reason string, command []string, deadline time.Time) (Approval, error) {
	var approval Approval
	var err error
	callErr := c.call(func(o *owner) {
		if o.stopping {
			err = ErrClosed
			return
		}
		if o.approvals == nil {
			o.approvals = map[string]*approvalRecord{}
		}
		o.approvalSeq++
		approval = Approval{
			ID: fmt.Sprintf("appr-%d", o.approvalSeq), Actor: actor, ActionKind: actionKind,
			Command: append([]string(nil), command...), Reason: reason, Deadline: deadline.UTC(),
		}
		o.approvals[approval.ID] = &approvalRecord{Approval: approval}
		o.record("", event.Event{Source: "GHOST", Kind: event.KindStatus, Message: "approval requested", Metadata: map[string]string{"approval_id": approval.ID, "action_kind": actionKind, "actor": actor}}, nil)
		o.publishBoundary()
	})
	return approval, errors.Join(err, callErr)
}

// RespondApproval records the first decision for an approval.
// The same decision again returns that record. A different decision conflicts.
func (c *Controller) RespondApproval(id, decision, responder, note string) (Approval, error) {
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != "approve" && decision != "deny" {
		return Approval{}, errors.New("decision must be approve or deny")
	}
	var approval Approval
	var err error
	callErr := c.call(func(o *owner) {
		approval, err = o.resolveApproval(id, decision, responder, note, "")
		o.publishBoundary()
	})
	return approval, errors.Join(err, callErr)
}

func (o *owner) resolveApproval(id, decision, responder, note, reason string) (Approval, error) {
	record := o.approvals[id]
	if record == nil {
		return Approval{}, fmt.Errorf("approval %q not found", id)
	}
	if record.resolved {
		if record.Decision != decision {
			return record.Approval, fmt.Errorf("approval %q already resolved", id)
		}
		return record.Approval, nil
	}
	if !record.Deadline.IsZero() && time.Now().After(record.Deadline) {
		decision, responder, reason = "deny", "ghost", "expired"
	}
	record.resolved = true
	record.Decision = decision
	record.Responder = responder
	record.Note = note
	record.ResolveReason = reason
	o.record("", event.Event{Source: "GHOST", Kind: event.KindStatus, Message: "approval " + decision, Metadata: map[string]string{"approval_id": id, "decision": decision, "responder": responder, "reason": reason}}, nil)
	return record.Approval, nil
}

func (o *owner) denyPendingApprovals(reason string) {
	for id := range o.approvals {
		record := o.approvals[id]
		if record.resolved {
			continue
		}
		_, _ = o.resolveApproval(id, "deny", "ghost", "", reason)
	}
}
