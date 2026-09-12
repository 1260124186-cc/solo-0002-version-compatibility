package resolution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"solo-0002-version-compatibility/internal/domain"
)

type catalogFingerprint struct {
	Components map[string]catalogComponent `json:"components"`
}

type catalogComponent struct {
	Releases map[string]catalogRelease `json:"releases"`
}

type catalogRelease struct {
	State    string            `json:"state"`
	Requires map[string]string `json:"requires"`
}

// fingerprint is deterministic and describes the solution-relevant catalog.
// Together with Catalog.Revision it makes continuation tokens unusable after
// any catalog change, without storing process-local state on the server.
func fingerprint(catalog domain.Catalog) string {
	value := catalogFingerprint{Components: make(map[string]catalogComponent, len(catalog.Components))}
	for _, id := range domain.SortedKeys(catalog.Components) {
		releases := make(map[string]catalogRelease, len(catalog.Releases[id]))
		for _, version := range domain.SortedKeys(catalog.Releases[id]) {
			release := catalog.Releases[id][version]
			releases[version] = catalogRelease{State: release.State, Requires: release.Requires}
		}
		value.Components[id] = catalogComponent{Releases: releases}
	}
	data, err := json.Marshal(value)
	if err != nil {
		// The structure above contains only strings and maps and cannot fail
		// with the catalog constraints enforced elsewhere.
		panic(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
