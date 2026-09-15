package epistemic

import (
	"fmt"
	"strings"

	ces "github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/event"
)

type group struct {
	title string
	items []itemRef
}

func (m Model) groups() []group {
	objects := func(kind ces.ObjectKind) []itemRef {
		var out []itemRef
		for i := range m.snapshot.Objects {
			if m.snapshot.Objects[i].Kind == kind {
				out = append(out, itemRef{object: &m.snapshot.Objects[i]})
			}
		}
		return out
	}
	var contradictions, relations []itemRef
	for i := range m.snapshot.Relations {
		item := itemRef{relation: &m.snapshot.Relations[i]}
		if strings.EqualFold(m.snapshot.Relations[i].Kind, "contradicts") {
			contradictions = append(contradictions, item)
		} else {
			relations = append(relations, item)
		}
	}
	return []group{
		{title: "HYPOTHESES", items: objects(ces.ObjectKindHypothesis)},
		{title: "UNKNOWNS", items: objects(ces.ObjectKindUnknown)},
		{title: "FRAMES", items: objects(ces.ObjectKindFrame)},
		{title: "EVIDENCE", items: append(objects(ces.ObjectKindObservation), objects(ces.ObjectKindClaim)...)},
		{title: "CONTRADICTIONS", items: contradictions},
		{title: "RELATIONS", items: relations},
	}
}

func (m Model) items() []itemRef {
	var out []itemRef
	for _, g := range m.groups() {
		out = append(out, g.items...)
	}
	return out
}

func itemKey(item itemRef) string {
	if item.object != nil {
		return "object:" + string(item.object.ID) + ":" + item.object.Alias
	}
	if item.relation != nil {
		return "relation:" + string(item.relation.ID) + ":" + item.relation.Alias
	}
	return ""
}

func (m Model) renderLines() ([]string, []int) {
	if m.detail {
		return m.detailLines(), nil
	}
	phase := strings.ToUpper(string(m.snapshot.Operator.Phase))
	if phase == "" {
		phase = "—"
	}
	work := strings.ToUpper(string(m.snapshot.Operator.WorkStatus))
	if work == "" {
		work = "—"
	}
	lines := []string{"EPISTEMIC STATE    " + phase, "WORK " + work}
	if m.snapshot.Operator.ReopenCount > 0 {
		lines = append(lines, fmt.Sprintf("REOPEN %d", m.snapshot.Operator.ReopenCount))
	}
	if m.snapshot.Supplement.TerminalReason != "" {
		lines = append(lines, "REASON "+m.snapshot.Supplement.TerminalReason)
	}
	if transition := m.snapshot.Operator.LastTransition; transition != nil {
		route := strings.ToUpper(string(transition.From)) + " -> " + strings.ToUpper(string(transition.To))
		lines = append(lines, "LAST "+route)
		if transition.Reason != "" {
			lines = append(lines, "REASON "+transition.Reason)
		}
	}
	var itemRows []int
	index := 0
	for _, g := range m.groups() {
		lines = append(lines, g.title)
		if len(g.items) == 0 {
			lines = append(lines, "  none")
			continue
		}
		for _, item := range g.items {
			marker := " "
			if index == m.selected {
				marker = "▶"
			}
			itemRows = append(itemRows, len(lines))
			lines = append(lines, marker+" "+m.overviewLine(item))
			index++
		}
	}
	lines = append(lines, "COGNITIVE TRAJECTORY")
	if len(m.trajectory) == 0 {
		lines = append(lines, "  none")
	} else {
		for _, item := range m.trajectory {
			for _, line := range trajectoryLines(item, max(m.width-6, 1)) {
				lines = append(lines, "  "+line)
			}
		}
	}
	return lines, itemRows
}

func trajectoryLines(item event.Event, width int) []string {
	get := func(key string) string { return strings.TrimSpace(item.Metadata[key]) }
	summary := get("summary")
	if summary == "" {
		summary = strings.TrimSpace(item.Message)
	}
	var headline, reason string
	switch item.Kind {
	case event.KindPhase:
		headline = "PHASE " + route(get("from"), get("to"))
	case event.KindHypothesis:
		headline = "HYPOTHESIS " + strings.ToUpper(get("status"))
		if alias := get("alias"); alias != "" {
			headline += " " + alias
		}
		reason = summary
	case event.KindReject:
		headline, reason = "HYPOTHESIS REJECTED", summary
	case event.KindFrame:
		headline, reason = "FRAME ACTIVATED", summary
	case event.KindAction:
		headline, reason = "ACTION", summary
	case event.KindVerify:
		headline, reason = "VERIFICATION", summary
	case event.KindContradict:
		headline = "CONTRADICTION"
		if kind := get("target_kind"); kind != "" {
			headline += " " + strings.ToUpper(kind) + " INVALIDATED"
		}
		reason = get("reason")
		if reason == "" {
			reason = summary
		}
	case event.KindReopen:
		headline = "REOPEN " + route(get("from"), get("to"))
		if count := get("reopen_count"); count != "" {
			if limit := get("reopen_limit"); limit != "" {
				count += "/" + limit
			}
			headline += " " + count
		}
		reason = get("reason")
	case event.KindComplete:
		headline = "WORK COMPLETE"
		reason = summary
	case event.KindIncomplete:
		headline = "WORK INCOMPLETE"
		reason = get("reason")
		if reason == "" {
			reason = summary
		}
	}
	var source []string
	if !item.Time.IsZero() {
		source = append(source, item.Time.Format("15:04:05"))
	}
	if item.Source != "" {
		source = append(source, strings.ToUpper(item.Source))
	}
	prefix := strings.Join(source, " ")
	headlineWidth := max(width-len([]rune(prefix))-2, 1)
	lines := wrapText(headline, headlineWidth)
	if prefix != "" && len(lines) > 0 {
		lines[0] = prefix + "  " + lines[0]
	}
	if reason != "" {
		for _, line := range wrapText(reason, width) {
			lines = append(lines, "  "+line)
		}
	}
	return lines
}

func route(from, to string) string {
	if from == "" || to == "" {
		return strings.ToUpper(strings.TrimSpace(from + " " + to))
	}
	return strings.ToUpper(from) + " -> " + strings.ToUpper(to)
}

func wrapText(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := words[0]
	for _, word := range words[1:] {
		if len([]rune(line))+1+len([]rune(word)) > width {
			lines = append(lines, line)
			line = word
		} else {
			line += " " + word
		}
	}
	return append(lines, line)
}

func (m Model) overviewLine(item itemRef) string {
	if item.object != nil {
		object := item.object
		label := object.Label
		if label == "" {
			label = object.Summary
		}
		if label == "" {
			label = object.Alias
		}
		return strings.TrimSpace(strings.Join([]string{object.Alias, label, strings.ToUpper(object.Status)}, "  "))
	}
	relation := item.relation
	left, right := relation.SourceAlias, relation.TargetAlias
	if left == "" {
		left = shortID(relation.SourceID)
	}
	if right == "" {
		right = shortID(relation.TargetID)
	}
	alias := relation.Alias
	if alias == "" {
		alias = "REL"
	}
	return fmt.Sprintf("%s  %s --%s--> %s  %s", alias, left, relation.Kind, right, strings.ToUpper(relation.Status))
}

func (m Model) detailLines() []string {
	items := m.items()
	if len(items) == 0 || m.selected < 0 || m.selected >= len(items) {
		return []string{"EPISTEMIC DETAIL", "No object selected."}
	}
	item := items[m.selected]
	if item.relation != nil {
		return m.relationLines(*item.relation)
	}
	object := item.object
	lines := []string{strings.ToUpper(string(object.Kind)), object.Alias, ""}
	if object.Summary != "" {
		lines = append(lines, object.Summary, "")
	} else if object.Label != "" {
		lines = append(lines, object.Label, "")
	}
	lines = appendField(lines, "STATE", strings.ToUpper(object.Status))
	lines = appendField(lines, "PRODUCED BY", object.ProducedBy)
	lines = appendField(lines, "ID", string(object.ID))
	lines = appendField(lines, "REVISION", object.Revision)
	if !object.CreatedAt.IsZero() {
		lines = appendField(lines, "CREATED", object.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	if object.Falsifier != "" {
		lines = append(lines, "", "FALSIFIER", "  "+object.Falsifier)
	}
	if object.Kind == ces.ObjectKindHypothesis {
		lines = appendRelationGroup(lines, "SUPPORT", m.objectRelations(*object, "supports"))
		lines = appendRelationGroup(lines, "COUNTEREVIDENCE", m.objectRelations(*object, "contradicts"))
	}
	return lines
}

func (m Model) relationLines(relation Relation) []string {
	left, right := relation.SourceAlias, relation.TargetAlias
	if left == "" {
		left = shortID(relation.SourceID)
	}
	if right == "" {
		right = shortID(relation.TargetID)
	}
	lines := []string{"RELATION", relation.Alias, "", fmt.Sprintf("%s --%s--> %s", left, relation.Kind, right), ""}
	lines = appendField(lines, "STATUS", strings.ToUpper(relation.Status))
	lines = appendField(lines, "SOURCE", relation.SourceSummary)
	lines = appendField(lines, "TARGET", relation.TargetSummary)
	lines = appendField(lines, "SOURCE ID", string(relation.SourceID))
	lines = appendField(lines, "TARGET ID", string(relation.TargetID))
	lines = appendField(lines, "PRODUCED BY", relation.ProducedBy)
	lines = appendField(lines, "ID", string(relation.ID))
	if strings.EqualFold(relation.Status, "retracted") {
		lines = appendField(lines, "RETRACTED BECAUSE", relation.RetractionReason)
	}
	return lines
}

func (m Model) objectRelations(object Object, kind string) []string {
	var out []string
	for _, relation := range m.snapshot.Relations {
		if !strings.EqualFold(relation.Kind, kind) || !relationTargets(relation, object) {
			continue
		}
		alias := relation.SourceAlias
		if alias == "" {
			alias = shortID(relation.SourceID)
		}
		line := fmt.Sprintf("%s  %s", alias, relation.SourceSummary)
		if relation.Status != "" {
			line += "  [" + strings.ToUpper(relation.Status) + "]"
		}
		out = append(out, line)
	}
	return out
}

func relationTargets(relation Relation, object Object) bool {
	return (object.ID != "" && relation.TargetID == object.ID) ||
		(object.Alias != "" && relation.TargetAlias == object.Alias)
}

func appendRelationGroup(lines []string, title string, relations []string) []string {
	lines = append(lines, "", title)
	if len(relations) == 0 {
		return append(lines, "  none")
	}
	for _, relation := range relations {
		lines = append(lines, "  "+relation)
	}
	return lines
}

func appendField(lines []string, label, value string) []string {
	if strings.TrimSpace(value) == "" {
		return lines
	}
	return append(lines, label+"  "+value)
}

func shortID(id ces.ID) string {
	if id == "" {
		return "?"
	}
	runes := []rune(id)
	if len(runes) > 6 {
		return "…" + string(runes[len(runes)-6:])
	}
	return string(runes)
}
