package domain

// RootDiff compares complete root requirement sets rather than resolved versions.
// A changed constraint is reported even when it resolves to the same release.
func RootDiff(before, after map[string]string) []Change {
	ids := make(map[string]bool)
	for id := range before {
		ids[id] = true
	}
	for id := range after {
		ids[id] = true
	}
	changes := make([]Change, 0)
	for _, id := range SortedKeys(ids) {
		oldConstraint, newConstraint := before[id], after[id]
		if oldConstraint == newConstraint {
			continue
		}
		kind := "change"
		switch {
		case oldConstraint == "":
			kind = "add"
		case newConstraint == "":
			kind = "remove"
		}
		changes = append(changes, Change{ComponentID: id, From: oldConstraint, To: newConstraint, Kind: kind})
	}
	return changes
}
