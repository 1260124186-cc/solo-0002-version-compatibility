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
		kind := domain.ChangeUpgrade
		switch {
		case oldVersion == "":
			kind = domain.ChangeAdd
		case newVersion == "":
			kind = domain.ChangeRemove
		default:
			oldParsed, oldErr := semver.Parse(oldVersion)
			newParsed, newErr := semver.Parse(newVersion)
			if oldErr == nil && newErr == nil && newParsed.Compare(oldParsed) < 0 {
				kind = domain.ChangeDowngrade
			}
		}
		changes = append(changes, domain.Change{ComponentID: id, From: oldVersion, To: newVersion, Kind: kind})
	}
	return changes
}
