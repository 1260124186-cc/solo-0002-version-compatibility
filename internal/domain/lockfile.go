package domain

import "time"

// LockfileFormat is the on-the-wire schema version of a lockfile document.
const LockfileFormat = 1

// LockReason records why a single selected version appears in the lock.
type LockReason struct {
	// Sources are the constraints that pinned this component, in deterministic
	// order. "root" denotes a root requirement; others are "component@version".
	Sources []LockConstraint `json:"sources"`
}

// LockConstraint is one constraint edge that justified a selection.
type LockConstraint struct {
	From       string `json:"from"`
	Constraint string `json:"constraint"`
}

// LockedRelease is the immutable dependency definition of a selected version.
// The release body is copied verbatim at lock time so that later catalog edits
// or withdrawals cannot rewrite what the lock was built from.
type LockedRelease struct {
	ComponentID string            `json:"component_id"`
	Version     string            `json:"version"`
	Requires    map[string]string `json:"requires"`
	State       string            `json:"state"`
}

// Lockfile is an immutable evidence record of one environment revision.
// It is never rewritten after creation and never drives a mutation.
type Lockfile struct {
	// Format is the document schema version.
	Format int `json:"format"`
	// ID is the globally unique identifier of this lockfile.
	ID string `json:"id"`
	// EnvironmentID names the environment the lock belongs to.
	EnvironmentID string `json:"environment_id"`
	// EnvironmentRevision is the exact environment revision captured.
	EnvironmentRevision uint64 `json:"environment_revision"`
	// CatalogRevision is the catalog revision the selection was resolved against.
	CatalogRevision uint64 `json:"catalog_revision"`
	// Roots are the full root requirement set at lock time.
	Roots map[string]string `json:"roots"`
	// Resolved is the exact component->version set that was applied.
	Resolved map[string]string `json:"resolved"`
	// Releases holds the immutable dependency definition of every selected version.
	Releases []LockedRelease `json:"releases"`
	// Reasons gives, per component, the constraint sources that justified it.
	Reasons map[string]LockReason `json:"reasons"`
	// Digest is the lowercase hex SHA-256 of the canonical payload, proving
	// the document was not altered after creation.
	Digest string `json:"digest"`
	// CreatedAt is the immutable creation timestamp.
	CreatedAt time.Time `json:"created_at"`
}

// LockRootChange explains how a root requirement moved versus the live
// environment. Constraint changes are reported even when the resolved set is
// unchanged because the lock captures root constraints verbatim.
type LockRootChange struct {
	ComponentID string `json:"component_id"`
	Locked      string `json:"locked"`
	Current     string `json:"current"`
	Kind        string `json:"kind"`
}

// LockDrift describes a read-only difference between a lockfile and the
// current catalog/environment. It never triggers a mutation.
type LockDrift struct {
	// DigestValid reports whether the lockfile's own content hash still matches.
	DigestValid bool `json:"digest_valid"`
	// EnvironmentExists reports whether the environment still exists.
	EnvironmentExists bool `json:"environment_exists"`
	// EnvironmentRevisionMatched reports whether the live environment is still
	// at exactly the revision the lock captured.
	EnvironmentRevisionMatched bool `json:"environment_matched_revision"`
	// EnvironmentMatched reports whether the environment still carries the
	// exact roots and resolved set recorded by the lock.
	EnvironmentMatched bool `json:"environment_matched"`
	// CatalogRevisionMatched reports whether the catalog is at the locked revision.
	CatalogRevisionMatched bool `json:"catalog_revision_matched"`
	// CatalogMatched reports whether every locked release still exists with an
	// identical definition and available state.
	CatalogMatched bool `json:"catalog_matched"`
	// CurrentEnvironmentRevision is the live revision (0 if the environment vanished).
	CurrentEnvironmentRevision uint64 `json:"current_environment_revision"`
	// CurrentCatalogRevision is the live catalog revision.
	CurrentCatalogRevision uint64 `json:"current_catalog_revision"`
	// EnvironmentChanges are version-set differences versus the live environment.
	EnvironmentChanges []Change `json:"environment_changes"`
	// RootChanges are root-constraint differences versus the live environment.
	RootChanges []LockRootChange `json:"root_changes"`
	// CatalogChanges lists per-component how the catalog drifted from the lock.
	CatalogChanges []LockCatalogChange `json:"catalog_changes"`
}

// LockCatalogChange explains how one locked component moved in the catalog.
type LockCatalogChange struct {
	ComponentID string `json:"component_id"`
	Locked      string `json:"locked"`
	// Kind is one of: identical, withdrawn, removed, definition_changed,
	// catalog_revision_ahead (release still identical).
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}
