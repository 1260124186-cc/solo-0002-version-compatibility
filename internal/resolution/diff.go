package resolution

import (
	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

func Diff(before, after map[string]string) []domain.Change {
	ids := make(map[string]bool)
	for id := range before {
		ids[id] = true
	}
	for id := range after {
		ids[id] = true
	}
	changes := make([]domain.Change, 0)
	for _, id := range domain.SortedKeys(ids) {
		oldVersion, newVersion := before[id], after[id]
		if oldVersion == newVersion {
			continue
		}
		kind := "upgrade"
		switch {
		case oldVersion == "":
			kind = "add"
		case newVersion == "":
			kind = "remove"
		default:
			oldParsed, oldErr := semver.Parse(oldVersion)
			newParsed, newErr := semver.Parse(newVersion)
			if oldErr == nil && newErr == nil && newParsed.Compare(oldParsed) < 0 {
				kind = "downgrade"
			}
		}
		changes = append(changes, domain.Change{ComponentID: id, From: oldVersion, To: newVersion, Kind: kind})
	}
	return changes
}

// DiffRoots compares desired root constraints against the current ones so a
// constraint-only adjustment stays visible even when versions do not move.
func DiffRoots(before, after map[string]string) []domain.RootChange {
	ids := make(map[string]bool)
	for id := range before {
		ids[id] = true
	}
	for id := range after {
		ids[id] = true
	}
	changes := make([]domain.RootChange, 0)
	for _, id := range domain.SortedKeys(ids) {
		oldConstraint, existed := before[id]
		newConstraint, wanted := after[id]
		switch {
		case !existed:
			changes = append(changes, domain.RootChange{ComponentID: id, To: newConstraint, Kind: "add"})
		case !wanted:
			changes = append(changes, domain.RootChange{ComponentID: id, From: oldConstraint, Kind: "remove"})
		case oldConstraint != newConstraint:
			changes = append(changes, domain.RootChange{ComponentID: id, From: oldConstraint, To: newConstraint, Kind: "constraint"})
		}
	}
	return changes
}
