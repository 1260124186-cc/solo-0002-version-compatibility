package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/semver"
)

// ImportRejectedError is returned when a confirmation cannot commit: the
// import is durably marked failed, the catalog is untouched, and the embedded
// record explains every entry-level error.
type ImportRejectedError struct {
	Import domain.Import
}

func (e *ImportRejectedError) Error() string {
	return fmt.Sprintf("import %s rejected with %d entry error(s)", e.Import.ID, e.Import.Summary.Errors)
}

// PreviewImportResult reports whether an import preview was created or an
// earlier identical import was replayed for idempotency.
type PreviewImportResult struct {
	Import  domain.Import `json:"import"`
	Created bool          `json:"created"`
}

var errImportReplayed = errors.New("identical import already exists")

// importFingerprint hashes the canonical manifest so identical resubmissions
// are recognized regardless of entry order, whitespace or requirement map
// ordering. Each row is keyed by component@version; an invalid batch can
// never complete, so malformed rows merely hash alongside the others.
func importFingerprint(inputs []domain.ImportEntryInput) string {
	type canonical struct {
		ComponentID string            `json:"component_id"`
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Version     string            `json:"version"`
		Requires    map[string]string `json:"requires"`
	}
	rows := make([]canonical, len(inputs))
	for i, in := range inputs {
		requires := in.Requires
		if requires == nil {
			requires = map[string]string{}
		}
		rows[i] = canonical{
			ComponentID: in.ComponentID,
			Name:        in.Name,
			Description: in.Description,
			Version:     in.Version,
			Requires:    requires,
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ComponentID != rows[j].ComponentID {
			return rows[i].ComponentID < rows[j].ComponentID
		}
		return rows[i].Version < rows[j].Version
	})
	data, _ := json.Marshal(rows)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type compBatch struct {
	id        string
	first     int
	creator   int // entry supplying the name; -1 when absent
	versions  map[string]int
	validID   bool
	inCatalog bool
}

type entryAnalysis struct {
	row  domain.ImportEntryResult
	errs []domain.ImportEntryError
}

func (a *entryAnalysis) addError(field, format string, args ...any) {
	a.errs = append(a.errs, domain.ImportEntryError{Field: field, Detail: fmt.Sprintf(format, args...)})
	a.row.Errors = a.errs
}

// analyzeEntries applies exactly the identifier, version and constraint rules
// enforced by the existing component/release creation paths, plus cross-entry
// rules: a dependency may be introduced later in the same batch and release
// capacity counts both catalog and batch versions.
func analyzeEntries(inputs []domain.ImportEntryInput, state *repository.State) []entryAnalysis {
	analyses := make([]entryAnalysis, len(inputs))
	groups := make(map[string]*compBatch)
	order := make([]*compBatch, 0)

	for i, in := range inputs {
		requires := in.Requires
		if requires == nil {
			requires = map[string]string{}
		}
		analyses[i] = entryAnalysis{row: domain.ImportEntryResult{
			Index:       i,
			ComponentID: in.ComponentID,
			Name:        in.Name,
			Description: in.Description,
			Version:     in.Version,
			Requires:    domain.CopyStrings(requires),
			Errors:      make([]domain.ImportEntryError, 0),
		}}
		group, exists := groups[in.ComponentID]
		if !exists {
			_, inCatalog := state.Catalog.Components[in.ComponentID]
			group = &compBatch{id: in.ComponentID, first: i, creator: -1, versions: make(map[string]int), inCatalog: inCatalog}
			groups[in.ComponentID] = group
			order = append(order, group)
		}
	}

	// Entry-local syntax and per-component metadata rules.
	for i, in := range inputs {
		a := &analyses[i]
		group := groups[in.ComponentID]

		idValid := false
		if err := domain.ValidateID(in.ComponentID); err != nil {
			a.addError("component_id", "%s", faultDetail(err))
		} else {
			idValid = true
		}
		versionValid := false
		if _, err := semver.Parse(in.Version); err != nil {
			a.addError("version", "%s", faultDetail(err))
		} else {
			versionValid = true
		}
		if len(in.Requires) > domain.MaxDependencies {
			a.addError("requires", "requirements must contain 0–%d entries", domain.MaxDependencies)
		}
		for dep, raw := range in.Requires {
			depValid := true
			if err := domain.ValidateID(dep); err != nil {
				a.addError("requires."+dep, "%s", faultDetail(err))
				depValid = false
			}
			if _, err := semver.ParseConstraint(raw); err != nil {
				a.addError("requires."+dep, "%s", faultDetail(err))
			}
			if idValid && depValid && dep == in.ComponentID {
				a.addError("requires."+dep, "a release cannot directly depend on its own component")
			}
		}
		if in.Name != "" {
			if err := domain.ValidateText(in.Name, "name", 1, 120); err != nil {
				a.addError("name", "%s", faultDetail(err))
			} else if idValid && group.creator == -1 {
				group.creator = i
			} else if idValid && group.creator != i {
				a.addError("name", "component metadata can only be supplied on one entry (%d)", group.creator)
			}
		}
		if err := domain.ValidateText(in.Description, "description", 0, 2000); err != nil {
			a.addError("description", "%s", faultDetail(err))
		}

		if versionValid {
			if previous, dup := group.versions[in.Version]; dup {
				a.addError("version", "component version repeats entry %d", previous)
			} else {
				group.versions[in.Version] = i
			}
		}
		if idValid {
			if group.inCatalog {
				if in.Name != "" {
					a.addError("name", "component %s already exists; an import cannot rename it", in.ComponentID)
				}
				if in.Description != "" {
					a.addError("description", "component %s already exists; an import cannot edit its description", in.ComponentID)
				}
			}
		}
	}

	// Mark groups whose identifier is actually valid so later checks never
	// treat malformed rows as creatable dependencies.
	for _, group := range order {
		group.validID = domain.ValidateID(group.id) == nil
	}

	// Component capacity: the overflow lands on the first entry of the first
	// new group that would exceed it, in manifest order.
	newSlots := domain.MaxComponents - len(state.Catalog.Components)
	for _, group := range order {
		if group.inCatalog || !group.validID {
			continue
		}
		if newSlots <= 0 {
			analyses[group.first].addError("component_id", "import would exceed the component limit of %d", domain.MaxComponents)
		}
		newSlots--
	}

	// Per-component release capacity, counted in manifest entry order.
	for _, group := range order {
		existing := len(state.Catalog.Releases[group.id])
		if !group.inCatalog {
			existing = 0
		}
		count := existing
		for i, in := range inputs {
			if in.ComponentID != group.id {
				continue
			}
			if _, err := semver.Parse(in.Version); err != nil {
				continue
			}
			if group.inCatalog {
				if _, present := state.Catalog.Releases[group.id][in.Version]; present {
					continue
				}
			}
			count++
			if count > domain.MaxReleases {
				analyses[i].addError("version", "component %s would exceed the release limit of %d", group.id, domain.MaxReleases)
			}
		}
	}

	// Cross-entry and catalog checks. These run before action assignment so
	// an error added to a previously clean row never keeps a stale action.
	for i, in := range inputs {
		a := &analyses[i]
		if len(a.errs) > 0 {
			continue
		}
		group := groups[in.ComponentID]
		if !group.inCatalog && group.creator == -1 {
			analyses[group.first].addError("name", "new component %s requires a name", group.id)
			continue
		}
		if group.inCatalog {
			existing, present := state.Catalog.Releases[in.ComponentID][in.Version]
			if present && !sameStringMap(existing.Requires, in.Requires) {
				a.addError("requires", "release %s@%s already exists with different requirements; releases are immutable", in.ComponentID, in.Version)
				continue
			}
		}
		for dep := range in.Requires {
			if _, exists := state.Catalog.Components[dep]; exists {
				continue
			}
			if batchGroup, inBatch := groups[dep]; inBatch && batchGroup.validID && !batchGroup.inCatalog {
				continue
			}
			a.addError("requires."+dep, "dependency component %s does not exist and is not created by this import", dep)
		}
	}

	// Action assignment only for rows that survived every rule.
	for i, in := range inputs {
		a := &analyses[i]
		if len(a.errs) > 0 {
			continue
		}
		group := groups[in.ComponentID]
		switch {
		case group.inCatalog:
			if _, present := state.Catalog.Releases[in.ComponentID][in.Version]; present {
				a.row.Action = domain.ImportAlreadyPresent
			} else {
				a.row.Action = domain.ImportAddRelease
			}
		case i == group.creator:
			a.row.Action = domain.ImportCreateComponent
		default:
			a.row.Action = domain.ImportAddRelease
		}
	}
	return analyses
}

func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func faultDetail(err error) string {
	var fault *domain.Fault
	if errors.As(err, &fault) {
		return fault.Detail
	}
	return err.Error()
}

func buildSummary(analyses []entryAnalysis) domain.ImportSummary {
	summary := domain.ImportSummary{Entries: len(analyses)}
	for i := range analyses {
		row := &analyses[i].row
		summary.Errors += len(row.Errors)
		switch row.Action {
		case domain.ImportCreateComponent:
			summary.CreateComponents++
		case domain.ImportAddRelease:
			summary.AddReleases++
		case domain.ImportAlreadyPresent:
			summary.AlreadyPresent++
		}
	}
	return summary
}

func (s *Service) PreviewImport(ctx context.Context, input domain.ImportInput) (PreviewImportResult, error) {
	if len(input.Entries) == 0 {
		return PreviewImportResult{}, domain.Invalid("entries must contain at least one item")
	}
	if len(input.Entries) > domain.MaxImportEntries {
		return PreviewImportResult{}, domain.Invalid("import accepts at most %d entries", domain.MaxImportEntries)
	}
	fingerprint := importFingerprint(input.Entries)

	var result PreviewImportResult
	err := s.repo.Update(ctx, func(state *repository.State) error {
		// Replaying a completed or still pending identical manifest is a
		// no-op; failed manifests may be resubmitted as a fresh preview.
		var replay domain.Import
		for i := range state.Imports {
			record := state.Imports[i]
			if record.Fingerprint != fingerprint {
				continue
			}
			if record.State == domain.ImportCompleted || record.State == domain.ImportPending {
				replay = record
				break
			}
		}
		if replay.ID != "" {
			result = PreviewImportResult{Import: replay, Created: false}
			return errImportReplayed
		}
		if len(state.Imports) >= domain.MaxImports {
			return domain.Limit("import capacity reached")
		}
		id, err := freshID("import-")
		if err != nil {
			return err
		}
		at := now()
		analyses := analyzeEntries(input.Entries, state)
		entries := make([]domain.ImportEntryResult, len(analyses))
		for i := range analyses {
			entries[i] = analyses[i].row
		}
		summary := buildSummary(analyses)
		record := domain.Import{
			ID:              id,
			Fingerprint:     fingerprint,
			State:           domain.ImportPending,
			CatalogRevision: state.Catalog.Revision,
			Summary:         summary,
			Entries:         entries,
			CreatedAt:       at,
		}
		action := "previewed"
		if summary.Errors > 0 {
			record.State = domain.ImportFailed
			record.CatalogRevision = 0
			failedAt := at
			record.FailedAt = &failedAt
			action = "rejected"
		}
		state.Imports[id] = record
		state.Record("import", id, action, at)
		result = PreviewImportResult{Import: record, Created: true}
		return nil
	})
	if errors.Is(err, errImportReplayed) {
		err = nil
	}
	return result, err
}

func (s *Service) Import(ctx context.Context, id string) (domain.Import, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Import{}, err
	}
	record, exists := state.Imports[id]
	if !exists {
		return record, domain.Missing("import", id)
	}
	return record, nil
}

func (s *Service) ListImports(ctx context.Context, phase string) ([]domain.Import, error) {
	if phase != "" && phase != domain.ImportPending && phase != domain.ImportCompleted && phase != domain.ImportFailed {
		return nil, domain.Invalid("unknown import state")
	}
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Import, 0, len(state.Imports))
	for _, record := range state.Imports {
		if phase == "" || record.State == phase {
			items = append(items, record)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

// ConfirmImport replays the preview against the current catalog inside a
// single transaction. Any invalid entry marks the import failed without
// touching the catalog; success creates the whole batch at once.
func (s *Service) ConfirmImport(ctx context.Context, id string) (domain.Import, error) {
	snapshot, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Import{}, err
	}
	existing, exists := snapshot.Imports[id]
	if !exists {
		return domain.Import{}, domain.Missing("import", id)
	}
	if existing.State == domain.ImportCompleted {
		return existing, nil
	}
	if existing.State == domain.ImportFailed {
		return existing, domain.Conflict("import %s failed; resubmit its manifest to create a new preview", id)
	}

	inputs := existing.Inputs()
	var completed domain.Import
	var rejected *ImportRejectedError
	err = s.repo.Commit(ctx, func(state *repository.State) error {
		latest, ok := state.Imports[id]
		if !ok {
			return domain.Missing("import", id)
		}
		if latest.State != domain.ImportPending {
			return domain.Conflict("import %s is %s", id, latest.State)
		}
		at := now()
		analyses := analyzeEntries(inputs, state)
		entries := make([]domain.ImportEntryResult, len(analyses))
		for i := range analyses {
			entries[i] = analyses[i].row
		}
		summary := buildSummary(analyses)
		if summary.Errors > 0 {
			failedAt := at
			failed := latest
			failed.State = domain.ImportFailed
			failed.Summary = summary
			failed.Entries = entries
			failed.CatalogRevision = 0
			failed.FailedAt = &failedAt
			state.Imports[id] = failed
			state.Record("import", id, "rejected", at)
			rejected = &ImportRejectedError{Import: failed}
			return nil
		}

		// New components first so every release dependency exists in the
		// candidate state before release validation runs.
		creating := make(map[string]domain.ImportEntryResult)
		for _, entry := range entries {
			if entry.Action == domain.ImportCreateComponent {
				creating[entry.ComponentID] = entry
			}
		}
		createdComponents := domain.SortedKeys(creating)
		for _, componentID := range createdComponents {
			entry := creating[componentID]
			state.Catalog.Components[componentID] = domain.Component{
				ID:          componentID,
				Name:        entry.Name,
				Description: entry.Description,
				CreatedAt:   at,
			}
			state.Catalog.Releases[componentID] = make(map[string]domain.Release)
			state.Catalog.Revision++
			state.Record("component", componentID, "created", at)
		}
		addedReleases := make([]string, 0, summary.CreateComponents+summary.AddReleases)
		for _, entry := range entries {
			if entry.Action == domain.ImportAlreadyPresent {
				continue
			}
			release := domain.Release{
				ComponentID: entry.ComponentID,
				Version:     entry.Version,
				Requires:    domain.CopyStrings(entry.Requires),
				State:       domain.Available,
				CreatedAt:   at,
			}
			state.Catalog.Releases[entry.ComponentID][entry.Version] = release
			state.Catalog.Revision++
			state.Record("release", entry.ComponentID+"@"+entry.Version, "added", at)
			addedReleases = append(addedReleases, entry.ComponentID+"@"+entry.Version)
		}
		doneAt := at
		completed = latest
		completed.State = domain.ImportCompleted
		completed.Summary = summary
		completed.Entries = entries
		completed.CreatedComponents = createdComponents
		completed.AddedReleases = addedReleases
		completed.CatalogRevision = state.Catalog.Revision
		completed.CompletedAt = &doneAt
		state.Imports[id] = completed
		state.Record("import", id, "completed", at)
		return nil
	})
	if err != nil {
		return domain.Import{}, err
	}
	if rejected != nil {
		return rejected.Import, rejected
	}
	return completed, nil
}
