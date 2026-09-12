package resolution

import (
	"context"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

type candidate struct {
	release      domain.Release
	version      semver.Version
	dependencies map[string]semver.Constraint
	blocked      map[string]bool
}

type requirement struct {
	from       string
	constraint semver.Constraint
}

func compile(ctx context.Context, c domain.Catalog) (map[string][]candidate, error) {
	result := make(map[string][]candidate, len(c.Components))
	for _, id := range domain.SortedKeys(c.Components) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		owner := c.Components[id]
		result[id] = make([]candidate, 0)
		for _, raw := range domain.SortedKeys(c.Releases[id]) {
			release := c.Releases[id][raw]
			if release.State != domain.Available {
				continue
			}
			version, err := semver.Parse(raw)
			if err != nil {
				return nil, err
			}
			dependencies := make(map[string]semver.Constraint)
			blocked := make(map[string]bool)
			for _, dep := range domain.SortedKeys(release.Requires) {
				constraint, err := semver.ParseConstraint(release.Requires[dep])
				if err != nil {
					return nil, err
				}
				dependencies[dep] = constraint
				if target, exists := c.Components[dep]; exists && !domain.CanReference(id, owner, target) {
					blocked[dep] = true
				}
			}
			result[id] = append(result[id], candidate{release: release, version: version, dependencies: dependencies, blocked: blocked})
		}
		sort.Slice(result[id], func(i, j int) bool {
			return result[id][i].version.Compare(result[id][j].version) > 0
		})
	}
	return result, nil
}

func matchesAll(version semver.Version, needs []requirement) bool {
	for _, need := range needs {
		if !need.constraint.Matches(version) {
			return false
		}
	}
	return true
}
