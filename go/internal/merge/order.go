package merge

import (
	"fmt"
	"sort"
	"strings"
)

// rootParent is the sibling bucket for records with no parent. It cannot
// collide with a real parent id, which is eight hex characters.
const rootParent = "\x00root"

func parentKey(e *entry) string {
	raw := e.get("parent")
	if nilValue(raw) {
		return rootParent
	}
	return canonical(raw)
}

// restoreRequiredAncestors keeps the minimal ancestor chain for a record the
// merge decided to keep.
//
// Deleting a subtree on one side while the other edits a descendant keeps the
// edited descendant by policy. Without the chain, that safe resurrection would
// produce an invalid dangling parent — and the whole merge would then fail
// validation over a record it deliberately saved.
func restoreRequiredAncestors(merged *index, baseByID, oursByID, theirsByID *index, events *eventLog) {
	for {
		missing := make([]string, 0)
		seen := map[string]bool{}
		for _, id := range merged.order {
			parent := merged.get(id).str("parent")
			if parent == "" || merged.has(parent) || seen[parent] {
				continue
			}
			seen[parent] = true
			missing = append(missing, parent)
		}
		if len(missing) == 0 {
			return
		}
		restored := false
		for _, id := range missing {
			source := oursByID.get(id)
			if source == nil {
				source = theirsByID.get(id)
			}
			if source == nil {
				source = baseByID.get(id)
			}
			if source == nil {
				continue
			}
			merged.put(id, source.clone())
			events.add(Event{ID: id, Decision: DecisionRestoredAncestor})
			restored = true
		}
		if !restored {
			return
		}
	}
}

// logOrderConflicts reports — without changing the outcome — that both sides
// reordered one parent's children differently. Ours' order is kept; the event
// exists so the audit log says the other device's arrangement was discarded.
func logOrderConflicts(merged *index, base, ours, theirs []*entry, events *eventLog) {
	indexes := []*index{indexByID(base), indexByID(ours), indexByID(theirs)}
	common := make([]string, 0)
	for _, id := range indexes[0].order {
		if indexes[1].has(id) && indexes[2].has(id) {
			common = append(common, id)
		}
	}
	stable := map[string]bool{}
	stableOrder := make([]string, 0, len(common))
	for _, id := range common {
		if !merged.has(id) {
			continue
		}
		first := parentKey(indexes[0].get(id))
		if parentKey(indexes[1].get(id)) != first || parentKey(indexes[2].get(id)) != first {
			continue
		}
		stable[id] = true
		stableOrder = append(stableOrder, id)
	}
	// The parents the stable ids sit under, first-seen order, deduplicated by
	// value exactly as Ruby's `uniq` does.
	type parentRef struct{ key, name string }
	parents := make([]parentRef, 0)
	seen := map[string]bool{}
	for _, id := range stableOrder {
		candidate := indexes[0].get(id)
		key := parentKey(candidate)
		if seen[key] {
			continue
		}
		seen[key] = true
		name := "root"
		if key != rootParent {
			if decoded, ok := decodeString(candidate.get("parent")); ok {
				name = decoded
			} else {
				name = string(candidate.get("parent"))
			}
		}
		parents = append(parents, parentRef{key: key, name: name})
	}

	for _, parent := range parents {
		sequences := make([][]string, 0, 3)
		for _, sideRecords := range [][]*entry{base, ours, theirs} {
			sequence := make([]string, 0)
			for _, candidate := range sideRecords {
				id := candidate.id()
				if id != "" && stable[id] && parentKey(candidate) == parent.key {
					sequence = append(sequence, id)
				}
			}
			sequences = append(sequences, sequence)
		}
		baseOrder, oursOrder, theirsOrder := sequences[0], sequences[1], sequences[2]
		if len(baseOrder) < 2 {
			continue
		}
		if sameOrder(oursOrder, baseOrder) || sameOrder(theirsOrder, baseOrder) || sameOrder(oursOrder, theirsOrder) {
			continue
		}
		events.add(Event{ID: parent.name, Decision: DecisionOursOrderingConflict})
	}
}

func sameOrder(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// orderRecords lays the merged records out as a file: the meta record, then a
// depth-first walk from the roots. The walk is what guarantees a parent is
// always written before its children, whichever side each record came from —
// and the leftover check is what turns a cycle into a refusal rather than a
// silently truncated file.
func orderRecords(merged *index, ours, theirs, base []*entry) ([]*entry, error) {
	meta := findMeta(ours, theirs, base)
	oursRank := ranks(ours)
	theirsRank := ranks(theirs)
	baseRank := ranks(base)

	children := map[string][]*entry{}
	buckets := make([]string, 0)
	for _, id := range merged.order {
		candidate := merged.get(id)
		key := parentKey(candidate)
		if _, exists := children[key]; !exists {
			buckets = append(buckets, key)
		}
		children[key] = append(children[key], candidate)
	}
	for _, key := range buckets {
		siblings := children[key]
		sort.SliceStable(siblings, func(left, right int) bool {
			first := rankOf(siblings[left], oursRank, theirsRank, baseRank)
			second := rankOf(siblings[right], oursRank, theirsRank, baseRank)
			if first[0] != second[0] {
				return first[0] < second[0]
			}
			return first[1] < second[1]
		})
	}

	ordered := make([]*entry, 0, len(merged.order)+1)
	if meta != nil {
		ordered = append(ordered, meta)
	}
	var visit func(*entry)
	visit = func(candidate *entry) {
		ordered = append(ordered, candidate)
		for _, child := range children[canonical(candidate.get("id"))] {
			visit(child)
		}
	}
	for _, root := range children[rootParent] {
		visit(root)
	}

	placed := map[string]bool{}
	for _, candidate := range ordered {
		if id := candidate.id(); id != "" {
			placed[id] = true
		}
	}
	missing := make([]string, 0)
	for _, id := range merged.order {
		if !placed[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("merged records have missing or cyclic parents: %s", strings.Join(missing, ", "))
	}
	return ordered, nil
}

// rankOf is the sibling sort key: ours' position first, then theirs', then the
// base's. A record no side ranks sorts last. Ours-first ordering is a stated
// property of the merge — the device you are sitting at keeps its arrangement.
func rankOf(candidate *entry, oursRank, theirsRank, baseRank map[string]int) [2]int {
	id := candidate.id()
	if position, ok := oursRank[id]; ok {
		return [2]int{0, position}
	}
	if position, ok := theirsRank[id]; ok {
		return [2]int{1, position}
	}
	if position, ok := baseRank[id]; ok {
		return [2]int{2, position}
	}
	return [2]int{2, 1 << 30}
}

func ranks(records []*entry) map[string]int {
	positions := map[string]int{}
	for position, candidate := range records {
		// Ruby assigns unconditionally, so a duplicate id takes its LAST
		// position. A duplicate cannot reach here from a validated side, but
		// matching the rule keeps the two implementations reasoning alike.
		if id := candidate.id(); id != "" {
			positions[id] = position
		}
	}
	return positions
}

func findMeta(sides ...[]*entry) *entry {
	for _, records := range sides {
		for _, candidate := range records {
			if candidate.str("type") == "meta" {
				return candidate.clone()
			}
		}
	}
	return nil
}
