package comms

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/core"
)

// Delegate creates a child task owned by target. The child's CES policy can
// only be as strong as or stronger than the parent's policy. Tool permissions
// and resource limits are intersections of all applicable policies.
func (r *Router) Delegate(ctx context.Context, request DelegationRequest) (core.Task, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(request.Objective) == "" || strings.TrimSpace(request.ExpectedDeliverable) == "" {
		return core.Task{}, r.reject(request.Requester, "", &request.ParentTaskID, "delegation objective and expected deliverable are required", nil)
	}
	if request.Timeout < 0 {
		return core.Task{}, r.reject(request.Requester, "", &request.ParentTaskID, "delegation timeout cannot be negative", nil)
	}
	if request.RequiresApproval {
		return core.Task{}, r.reject(request.Requester, "", &request.ParentTaskID, "delegation requires human approval", nil)
	}
	if request.Requester != core.HumanAgentID {
		if _, ok := r.directory.Agent(request.Requester); !ok {
			return core.Task{}, r.reject(request.Requester, "", &request.ParentTaskID, "unknown requester", nil)
		}
	}
	target, ok := r.directory.Agent(request.Target)
	if !ok {
		return core.Task{}, r.reject(request.Requester, "", &request.ParentTaskID, "unknown delegation target", map[string]any{"target": request.Target})
	}
	if !r.directory.CanMessage(request.Requester, request.Target) {
		return core.Task{}, r.reject(request.Requester, "", &request.ParentTaskID, "delegation permission denied", map[string]any{"target": request.Target})
	}

	r.mu.Lock()
	parent, exists := r.tasks[request.ParentTaskID]
	r.mu.Unlock()
	if !exists {
		return core.Task{}, r.reject(request.Requester, "", &request.ParentTaskID, "unknown parent task", nil)
	}
	if parent.Status.IsTerminal() {
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "parent task is terminal", nil)
	}
	if request.Requester != parent.Owner && !(request.Requester == core.HumanAgentID && parent.Requester == core.HumanAgentID) {
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "requester does not own parent task", nil)
	}
	if request.Target.Validate() != nil {
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "invalid delegation target", nil)
	}
	if parent.RequiresApproval {
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "parent task requires human approval", nil)
	}
	requesterPolicy := core.PermissionPolicy{Mode: core.PermissionModeInherit}
	if request.Requester != core.HumanAgentID {
		requester, _ := r.directory.Agent(request.Requester)
		requesterPolicy = requester.PermissionPolicy
	}
	parentPolicy := core.PermissionPolicy{Mode: core.PermissionModeInherit}
	if parent.PermissionPolicy != nil {
		parentPolicy = *parent.PermissionPolicy
	}
	requestedPolicy := core.PermissionPolicy{Mode: core.PermissionModeInherit}
	if request.PermissionPolicy != nil {
		requestedPolicy = *request.PermissionPolicy
	}
	permissions, approval, err := intersectPermissions(parentPolicy, requesterPolicy, target.PermissionPolicy, requestedPolicy)
	if err != nil {
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, err.Error(), nil)
	}
	if approval {
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "permission requires human approval", nil)
	}
	policy := parent.CESPolicy
	if request.CESPolicy != nil {
		if cesRank(*request.CESPolicy) == -1 || cesRank(*request.CESPolicy) < cesRank(parent.CESPolicy) {
			return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "CES policy override cannot weaken the parent policy", nil)
		}
		policy = *request.CESPolicy
	}
	if request.Limits != nil && !validTaskLimits(*request.Limits) {
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "delegation resource limits are invalid", nil)
	}

	r.mu.Lock()
	parent, exists = r.tasks[request.ParentTaskID]
	if !exists || parent.Status.IsTerminal() {
		r.mu.Unlock()
		return core.Task{}, r.reject(request.Requester, "", &request.ParentTaskID, "parent task is no longer open", nil)
	}
	depth := parent.Depth + 1
	maxDepth := r.limits.MaxDelegationDepth
	if parent.Limits.MaxDelegationDepth > 0 && parent.Limits.MaxDelegationDepth < maxDepth {
		maxDepth = parent.Limits.MaxDelegationDepth
	}
	if depth > maxDepth {
		r.mu.Unlock()
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "maximum delegation depth exceeded", map[string]any{"depth": depth, "limit": maxDepth})
	}
	if visits := r.ancestorVisitsLocked(parent.ID, request.Target); visits >= r.limits.MaxAgentVisits {
		r.mu.Unlock()
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "delegation cycle limit exceeded", map[string]any{"target": request.Target, "visits": visits})
	}
	if parent.Limits.MaxParallel > 0 && r.openChildrenLocked(parent.ID) >= parent.Limits.MaxParallel {
		r.mu.Unlock()
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "parent task parallel delegation limit reached", map[string]any{"limit": parent.Limits.MaxParallel})
	}
	key := delegationKey(request.Requester, request.Target, request.ParentTaskID, request.Objective)
	if priorID, found := r.openRequests[key]; found {
		if prior, ok := r.tasks[priorID]; ok && !prior.Status.IsTerminal() {
			r.mu.Unlock()
			return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "duplicate open delegation request", map[string]any{"task_id": priorID})
		}
	}
	if request.CausationID != "" && request.CausationID.Validate() != nil {
		r.mu.Unlock()
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "invalid causation ID", nil)
	}
	now := r.clock().UTC()
	limits := intersectTaskLimits(parent.Limits, request.Limits, r.limits)
	timeout := r.limits.DefaultTimeout
	if request.Timeout > 0 && request.Timeout < timeout {
		timeout = request.Timeout
	}
	deadline := now.Add(timeout)
	if limits.Budget.Deadline != nil && limits.Budget.Deadline.Before(deadline) {
		deadline = *limits.Budget.Deadline
	}
	if limits.Budget.MaxDuration > 0 && now.Add(limits.Budget.MaxDuration).Before(deadline) {
		deadline = now.Add(limits.Budget.MaxDuration)
	}
	if !deadline.After(now) {
		r.mu.Unlock()
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "delegation deadline has expired", nil)
	}
	limits.Budget.Deadline = &deadline
	id, err := r.ids.New("task")
	if err != nil {
		r.mu.Unlock()
		return core.Task{}, err
	}
	parentID := parent.ID
	child := core.Task{
		ID: core.TaskID(id), Requester: request.Requester, Owner: request.Target,
		Parent: &parentID, Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now,
		CESPolicy: policy, Limits: limits, ThreadID: parent.ThreadID,
		Depth: depth, Objective: strings.TrimSpace(request.Objective),
		ContextRefs:         append([]string(nil), request.ContextRefs...),
		ExpectedDeliverable: strings.TrimSpace(request.ExpectedDeliverable),
		PermissionPolicy:    &permissions, CorrelationID: parent.CorrelationID,
	}
	if err := child.Validate(); err != nil {
		r.mu.Unlock()
		return core.Task{}, r.reject(request.Requester, parent.ThreadID, &request.ParentTaskID, "invalid child task: "+err.Error(), nil)
	}
	event, err := r.emitLocked("task.created", actorFor(request.Requester), child.ID, child.Owner, child.ThreadID, parent.CorrelationID, request.CausationID, map[string]any{"task": child, "parent_task_id": parent.ID})
	if err != nil {
		r.mu.Unlock()
		return core.Task{}, err
	}
	if child.CorrelationID == "" {
		child.CorrelationID = event.CorrelationID
	}
	r.tasks[child.ID] = cloneTask(child)
	r.taskOrder = append(r.taskOrder, child.ID)
	r.taskEvents[child.ID] = event.ID
	r.openRequests[key] = child.ID
	r.mu.Unlock()

	parentMessage := core.TaskID(child.ID)
	message := core.Message{
		Sender: request.Requester, Recipients: []core.AgentID{request.Target},
		ThreadID: child.ThreadID, TaskID: &parentMessage, Type: core.MessageTaskDelegation,
		Content: child.Objective, CausationID: event.ID, CorrelationID: child.CorrelationID,
	}
	if _, _, err := r.acceptMessage(message, true); err != nil {
		_, _ = r.Fail(context.Background(), child.ID, "delegation delivery could not be accepted")
		return core.Task{}, err
	}
	accepted := r.messageByTaskAndType(child.ID, core.MessageTaskDelegation)
	if accepted.ID != "" {
		for _, recipient := range accepted.Recipients {
			_ = r.tryDeliver(ctx, accepted.ID, recipient, r.messageAcceptedID(accepted.ID))
		}
	}
	return r.task(child.ID), nil
}

func (r *Router) ancestorVisitsLocked(parentID core.TaskID, target core.AgentID) int {
	visits := 0
	seen := map[core.TaskID]bool{}
	for id := parentID; id != "" && !seen[id]; {
		seen[id] = true
		task, ok := r.tasks[id]
		if !ok {
			break
		}
		if task.Owner == target {
			visits++
		}
		if task.Parent == nil {
			break
		}
		id = *task.Parent
	}
	return visits
}

func (r *Router) openChildrenLocked(parentID core.TaskID) int {
	count := 0
	for _, id := range r.taskOrder {
		child := r.tasks[id]
		if child.Parent != nil && *child.Parent == parentID && !child.Status.IsTerminal() {
			count++
		}
	}
	return count
}

func (r *Router) task(id core.TaskID) core.Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneTask(r.tasks[id])
}

func (r *Router) messageByTaskAndType(taskID core.TaskID, kind core.MessageType) core.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.messageOrder) - 1; i >= 0; i-- {
		message := r.messages[r.messageOrder[i]]
		if taskID == taskIDOf(message.TaskID) && message.Type == kind {
			return cloneMessage(message)
		}
	}
	return core.Message{}
}

func taskIDOf(id *core.TaskID) core.TaskID {
	if id == nil {
		return ""
	}
	return *id
}

func delegationKey(requester, target core.AgentID, parent core.TaskID, objective string) string {
	return pairKey(requester, target) + "/" + string(parent) + "/" + normaliseObjective(objective)
}

func cesRank(policy core.CESPolicy) int {
	switch policy {
	case core.CESOff:
		return 0
	case core.CESObserve:
		return 1
	case core.CESEnforce:
		return 2
	default:
		return -1
	}
}

func intersectPermissions(policies ...core.PermissionPolicy) (core.PermissionPolicy, bool, error) {
	allowSets := make([]map[string]bool, 0, len(policies))
	denied := map[string]bool{}
	approval := false
	for _, policy := range policies {
		if policy.Mode == "" {
			policy.Mode = core.PermissionModeInherit
		}
		if err := policy.Validate(); err != nil {
			return core.PermissionPolicy{}, false, err
		}
		if policy.Mode == core.PermissionModeAsk {
			approval = true
		}
		if policy.Mode == core.PermissionModeAllowList {
			set := map[string]bool{}
			for _, tool := range policy.AllowTools {
				set[tool] = true
			}
			allowSets = append(allowSets, set)
		}
		if policy.Mode == core.PermissionModeDenyList {
			for _, tool := range policy.DenyTools {
				denied[tool] = true
			}
		}
	}
	if approval {
		return core.PermissionPolicy{Mode: core.PermissionModeAsk}, true, nil
	}
	if len(allowSets) > 0 {
		first := allowSets[0]
		allowed := make([]string, 0, len(first))
		for tool := range first {
			if denied[tool] {
				continue
			}
			inAll := true
			for _, set := range allowSets[1:] {
				if !set[tool] {
					inAll = false
					break
				}
			}
			if inAll {
				allowed = append(allowed, tool)
			}
		}
		sort.Strings(allowed)
		return core.PermissionPolicy{Mode: core.PermissionModeAllowList, AllowTools: allowed}, false, nil
	}
	if len(denied) == 0 {
		return core.PermissionPolicy{Mode: core.PermissionModeInherit}, false, nil
	}
	denials := make([]string, 0, len(denied))
	for tool := range denied {
		denials = append(denials, tool)
	}
	sort.Strings(denials)
	return core.PermissionPolicy{Mode: core.PermissionModeDenyList, DenyTools: denials}, false, nil
}

func validTaskLimits(limits core.TaskLimits) bool {
	return limits.MaxDelegationDepth >= 0 && limits.MaxParallel >= 0 && limits.MaxMessages >= 0 &&
		limits.Budget.MaxTokens >= 0 && limits.Budget.MaxCost >= 0 &&
		!math.IsNaN(limits.Budget.MaxCost) && !math.IsInf(limits.Budget.MaxCost, 0) &&
		limits.Budget.MaxDuration >= 0 && (limits.Budget.Deadline == nil || !limits.Budget.Deadline.IsZero())
}

func intersectTaskLimits(parent core.TaskLimits, override *core.TaskLimits, global Limits) core.TaskLimits {
	result := parent
	if override != nil {
		result.MaxDelegationDepth = minPositive(result.MaxDelegationDepth, override.MaxDelegationDepth)
		result.MaxParallel = minPositive(result.MaxParallel, override.MaxParallel)
		result.MaxMessages = minPositive(result.MaxMessages, override.MaxMessages)
		result.Budget.MaxTokens = minPositiveInt64(result.Budget.MaxTokens, override.Budget.MaxTokens)
		result.Budget.MaxCost = minPositiveFloat(result.Budget.MaxCost, override.Budget.MaxCost)
		result.Budget.MaxDuration = minPositiveDuration(result.Budget.MaxDuration, override.Budget.MaxDuration)
		result.Budget.Deadline = earlierDeadline(result.Budget.Deadline, override.Budget.Deadline)
	}
	result.MaxDelegationDepth = minPositive(result.MaxDelegationDepth, global.MaxDelegationDepth)
	result.MaxMessages = minPositive(result.MaxMessages, global.MaxMessagesPerTask)
	return result
}

func minPositive(a, b int) int {
	if a == 0 || (b > 0 && b < a) {
		return b
	}
	return a
}

func minPositiveInt64(a, b int64) int64 {
	if a == 0 || (b > 0 && b < a) {
		return b
	}
	return a
}

func minPositiveFloat(a, b float64) float64 {
	if a == 0 || (b > 0 && b < a) {
		return b
	}
	return a
}

func minPositiveDuration(a, b time.Duration) time.Duration {
	if a == 0 || (b > 0 && b < a) {
		return b
	}
	return a
}

func earlierDeadline(a, b *time.Time) *time.Time {
	if a == nil {
		if b == nil {
			return nil
		}
		copy := *b
		return &copy
	}
	if b == nil || !b.Before(*a) {
		copy := *a
		return &copy
	}
	copy := *b
	return &copy
}
