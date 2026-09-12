package service

import (
	"context"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/manifest"
)

// EnvironmentManifest exports the environment's confirmed compatible set as
// a transferable manifest. It only reads a snapshot and never mutates state.
func (s *Service) EnvironmentManifest(ctx context.Context, id string) (domain.Manifest, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Manifest{}, err
	}
	env, exists := state.Environments[id]
	if !exists {
		return domain.Manifest{}, domain.Missing("environment", id)
	}
	return manifest.Build(domain.ManifestSourceEnvironment, id, state.Catalog.Revision, env.Roots, env.Resolved, state.Catalog)
}

// PlanManifest exports a confirmed plan (ready or applied) as a manifest.
// Draft and cancelled plans have no confirmed set to transfer.
func (s *Service) PlanManifest(ctx context.Context, id string) (domain.Manifest, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Manifest{}, err
	}
	plan, exists := state.Plans[id]
	if !exists {
		return domain.Manifest{}, domain.Missing("plan", id)
	}
	if plan.State != domain.Ready && plan.State != domain.Applied {
		return domain.Manifest{}, domain.Conflict("cannot export a %s plan as a manifest; validate it first", plan.State)
	}
	return manifest.Build(domain.ManifestSourcePlan, id, plan.CatalogRevision, plan.Roots, plan.Resolved, state.Catalog)
}

// VerifyManifest checks a submitted manifest against this instance's catalog
// and reports specific inconsistencies. It only reads a snapshot.
func (s *Service) VerifyManifest(ctx context.Context, m domain.Manifest) (domain.ManifestReport, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.ManifestReport{}, err
	}
	return manifest.Verify(m, state.Catalog)
}
