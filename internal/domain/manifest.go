package domain

// ManifestFormat is the only manifest layout this instance can generate or
// verify. A different format version means the canonical digest rules may
// differ, so verification refuses to interpret it.
const ManifestFormat = 1

const (
	ManifestSourceEnvironment = "environment"
	ManifestSourcePlan        = "plan"
)

// Manifest issue kinds reported by verification.
const (
	IssueUnsupportedFormat = "unsupported_format"
	IssueInvalidManifest   = "invalid_manifest"
	IssueContentCorrupted  = "content_corrupted"
	IssueMissingRelease    = "missing_release"
	IssueRequiresMismatch  = "requires_mismatch"
)

// ManifestRelease captures the immutable dependency definition of one
// release referenced by the resolved set.
type ManifestRelease struct {
	ComponentID string            `json:"component_id"`
	Version     string            `json:"version"`
	Requires    map[string]string `json:"requires"`
}

// Manifest is a transferable description of a confirmed compatible set. The
// digest detects content alteration after generation; it is not a signature
// and proves nothing about who produced the manifest. CatalogRevision is
// informational only: instances reconcile catalogs independently, so a
// revision difference never invalidates a manifest.
type Manifest struct {
	Format          int               `json:"format"`
	Source          string            `json:"source"`
	SourceID        string            `json:"source_id"`
	CatalogRevision uint64            `json:"catalog_revision"`
	Roots           map[string]string `json:"roots"`
	Resolved        map[string]string `json:"resolved"`
	Releases        []ManifestRelease `json:"releases"`
	Digest          string            `json:"digest"`
}

// ManifestIssue pinpoints one specific inconsistency found during
// verification. ComponentID and Version are set for release-level issues.
type ManifestIssue struct {
	Kind        string `json:"kind"`
	ComponentID string `json:"component_id,omitempty"`
	Version     string `json:"version,omitempty"`
	Detail      string `json:"detail"`
}

// ManifestReport is the outcome of verifying a submitted manifest against
// this instance's catalog. Verification is read-only.
type ManifestReport struct {
	Valid           bool            `json:"valid"`
	Format          int             `json:"format"`
	DigestMatch     bool            `json:"digest_match"`
	ReleasesChecked int             `json:"releases_checked"`
	Issues          []ManifestIssue `json:"issues"`
}
