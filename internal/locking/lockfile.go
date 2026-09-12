// Package locking builds, digests, verifies and compares immutable lockfiles.
// A lockfile is evidence of exactly what an environment revision resolved to.
// It never feeds a mutation back into the environment or catalog.
package locking

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"solo-0002-version-compatibility/internal/domain"
)

// canonicalPayload is the digest-covered body. Field order here is fixed and
// every map/slice inside is sorted by the builders, so the digest is stable
// regardless of Go map iteration.
type canonicalPayload struct {
	Format              int                          `json:"format"`
	ID                  string                       `json:"id"`
	EnvironmentID       string                       `json:"environment_id"`
	EnvironmentRevision uint64                       `json:"environment_revision"`
	CatalogRevision     uint64                       `json:"catalog_revision"`
	Roots               map[string]string            `json:"roots"`
	Resolved            map[string]string            `json:"resolved"`
	Releases            []domain.LockedRelease       `json:"releases"`
	Reasons             map[string]domain.LockReason `json:"reasons"`
	CreatedAt           string                       `json:"created_at"`
}

// Digest computes the canonical SHA-256 over every field except the digest.
func Digest(lock domain.Lockfile) (string, error) {
	payload := canonicalPayload{
		Format:              lock.Format,
		ID:                  lock.ID,
		EnvironmentID:       lock.EnvironmentID,
		EnvironmentRevision: lock.EnvironmentRevision,
		CatalogRevision:     lock.CatalogRevision,
		Roots:               lock.Roots,
		Resolved:            lock.Resolved,
		Releases:            lock.Releases,
		Reasons:             lock.Reasons,
		CreatedAt:           lock.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Verify recomputes the digest and confirms the document has not been altered.
func Verify(lock domain.Lockfile) error {
	computed, err := Digest(lock)
	if err != nil {
		return domain.Invalid("cannot recompute lockfile digest: %s", err)
	}
	if computed != lock.Digest {
		return domain.Invalid("lockfile %s is corrupted: digest does not match its contents", lock.ID)
	}
	if lock.Format != domain.LockfileFormat {
		return domain.Invalid("unsupported lockfile format %d", lock.Format)
	}
	return nil
}
