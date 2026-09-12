// Package manifest builds transferable compatibility manifests and verifies
// submitted manifests against the local catalog. All operations are pure
// functions over state snapshots and never mutate anything.
package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
)

// content is the canonical digest input: every manifest field except the
// digest itself, in exactly this field order. Map keys are sorted by
// encoding/json; the releases slice is sorted explicitly. JSON is encoded
// without HTML escaping so the form stays reproducible outside Go.
type content struct {
	Format          int                      `json:"format"`
	Source          string                   `json:"source"`
	SourceID        string                   `json:"source_id"`
	CatalogRevision uint64                   `json:"catalog_revision"`
	Roots           map[string]string        `json:"roots"`
	Resolved        map[string]string        `json:"resolved"`
	Releases        []domain.ManifestRelease `json:"releases"`
}

// Build assembles a manifest for a confirmed compatible set, pulling the
// dependency definitions of the referenced releases from the catalog.
// Releases are immutable, so definitions read now match resolution time.
func Build(source, sourceID string, catalogRevision uint64, roots, resolved map[string]string, catalog domain.Catalog) (domain.Manifest, error) {
	releases := make([]domain.ManifestRelease, 0, len(resolved))
	for _, id := range domain.SortedKeys(resolved) {
		version := resolved[id]
		release, exists := catalog.Releases[id][version]
		if !exists {
			return domain.Manifest{}, fmt.Errorf("resolved release %s@%s is missing from the catalog", id, version)
		}
		releases = append(releases, domain.ManifestRelease{ComponentID: id, Version: version, Requires: domain.CopyStrings(release.Requires)})
	}
	m := domain.Manifest{
		Format:          domain.ManifestFormat,
		Source:          source,
		SourceID:        sourceID,
		CatalogRevision: catalogRevision,
		Roots:           domain.CopyStrings(roots),
		Resolved:        domain.CopyStrings(resolved),
		Releases:        releases,
	}
	digest, err := Digest(m)
	if err != nil {
		return domain.Manifest{}, err
	}
	m.Digest = digest
	return m, nil
}

// Digest returns the content digest of a manifest. It detects alteration or
// corruption of the content; it is not an authentication mechanism and must
// not be used to establish who generated the manifest.
func Digest(m domain.Manifest) (string, error) {
	data, err := canonical(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonical(m domain.Manifest) ([]byte, error) {
	releases := make([]domain.ManifestRelease, 0, len(m.Releases))
	for _, release := range m.Releases {
		if release.Requires == nil {
			release.Requires = map[string]string{}
		}
		releases = append(releases, release)
	}
	sort.Slice(releases, func(i, j int) bool {
		if releases[i].ComponentID != releases[j].ComponentID {
			return releases[i].ComponentID < releases[j].ComponentID
		}
		return releases[i].Version < releases[j].Version
	})
	canonical := content{
		Format:          m.Format,
		Source:          m.Source,
		SourceID:        m.SourceID,
		CatalogRevision: m.CatalogRevision,
		Roots:           nonNil(m.Roots),
		Resolved:        nonNil(m.Resolved),
		Releases:        releases,
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(canonical); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func nonNil(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}
