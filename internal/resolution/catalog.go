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
			for _, dep := range domain.SortedKeys(release.Requires) {
				constraint, err := semver.ParseConstraint(release.Requires[dep])
				if err != nil {
					return nil, err
				}
				dependencies[dep] = constraint
			}
			result[id] = append(result[id], candidate{release: release, version: version, dependencies: dependencies})
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

// preferInstalled moves each installed version to the front of its
// component's candidate list so the search tries keeping it first. Unknown
// or withdrawn versions are absent from the candidates and ignored; the
// remaining candidates keep their newest-first order, so the overall
// selection order stays deterministic.
func preferInstalled(catalog map[string][]candidate, installed map[string]string) {
	for id, version := range installed {
		candidates := catalog[id]
		for i, choice := range candidates {
			if choice.release.Version != version {
				continue
			}
			if i > 0 {
				copy(candidates[1:i+1], candidates[:i])
				candidates[0] = choice
			}
			break
		}
	}
}
