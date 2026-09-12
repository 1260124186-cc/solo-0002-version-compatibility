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
	channel      string
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
			channel := release.Channel
			if channel == "" {
				channel = domain.ChannelStable
			}
			result[id] = append(result[id], candidate{release: release, version: version, channel: channel, dependencies: dependencies})
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

// withinChannel restricts candidates to the preferred channel and reports
// whether any candidate was dropped. Withdrawn releases were already removed
// by compile, so lifecycle state always takes precedence over channel.
func withinChannel(catalog map[string][]candidate, channel string) (map[string][]candidate, bool) {
	result := make(map[string][]candidate, len(catalog))
	dropped := false
	for _, id := range domain.SortedKeys(catalog) {
		kept := make([]candidate, 0, len(catalog[id]))
		for _, choice := range catalog[id] {
			if choice.channel == channel {
				kept = append(kept, choice)
			} else {
				dropped = true
			}
		}
		result[id] = kept
	}
	return result, dropped
}

// preferChannel keeps every candidate but tries preferred-channel versions
// first. Each group stays in descending version order, so the fallback search
// remains deterministic.
func preferChannel(catalog map[string][]candidate, channel string) map[string][]candidate {
	result := make(map[string][]candidate, len(catalog))
	for _, id := range domain.SortedKeys(catalog) {
		choices := catalog[id]
		ordered := make([]candidate, 0, len(choices))
		for _, choice := range choices {
			if choice.channel == channel {
				ordered = append(ordered, choice)
			}
		}
		for _, choice := range choices {
			if choice.channel != channel {
				ordered = append(ordered, choice)
			}
		}
		result[id] = ordered
	}
	return result
}
