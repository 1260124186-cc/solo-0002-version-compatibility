package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/resolution"
)

// ComputeMatrix solves every candidate version combination of two axes against
// one catalog snapshot. A combination without a solution only marks its own
// cell; the shared step budget and the request deadline bound the whole run.
func (s *Service) ComputeMatrix(ctx context.Context, input domain.MatrixInput) (domain.Matrix, error) {
	if err := domain.ValidateMatrixInput(input); err != nil {
		return domain.Matrix{}, err
	}
	snapshot, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Matrix{}, err
	}
	components := append([]string{input.First.Component, input.Second.Component}, domain.SortedKeys(input.Roots)...)
	for _, id := range components {
		if _, exists := snapshot.Catalog.Components[id]; !exists {
			return domain.Matrix{}, domain.Missing("component", id)
		}
	}
	session, err := s.solver.NewSession(ctx, snapshot.Catalog)
	if err != nil {
		return domain.Matrix{}, err
	}
	matrix := domain.Matrix{
		CatalogRevision: snapshot.Catalog.Revision,
		First:           domain.MatrixAxis{Component: input.First.Component, Versions: append([]string(nil), input.First.Versions...)},
		Second:          domain.MatrixAxis{Component: input.Second.Component, Versions: append([]string(nil), input.Second.Versions...)},
		Roots:           domain.CopyStrings(input.Roots),
		Cells:           make([]domain.MatrixCell, 0, len(input.First.Versions)*len(input.Second.Versions)),
		CreatedAt:       now(),
	}
	for _, firstVersion := range input.First.Versions {
		for _, secondVersion := range input.Second.Versions {
			cell, err := solveCell(ctx, session, snapshot.Catalog, input, firstVersion, secondVersion)
			if err != nil {
				return domain.Matrix{}, err
			}
			if cell.Compatible {
				matrix.Compatible++
			} else {
				matrix.Incompatible++
			}
			matrix.Cells = append(matrix.Cells, cell)
		}
	}
	matrix.Steps = session.Steps()
	if !input.Save {
		return matrix, nil
	}
	id, err := freshID("matrix")
	if err != nil {
		return domain.Matrix{}, err
	}
	matrix.ID = id
	matrix.Saved = true
	// The matrix is a historical record of one catalog view; concurrent
	// catalog changes do not block saving it.
	err = s.repo.Update(ctx, func(state *repository.State) error {
		if len(state.Matrices) >= domain.MaxMatrices {
			return domain.Limit("matrix capacity reached")
		}
		state.Matrices[id] = matrix
		state.Record("matrix", id, "computed", matrix.CreatedAt)
		return nil
	})
	if err != nil {
		return domain.Matrix{}, err
	}
	return matrix, nil
}

func solveCell(ctx context.Context, session *resolution.Session, catalog domain.Catalog, input domain.MatrixInput, firstVersion, secondVersion string) (domain.MatrixCell, error) {
	cell := domain.MatrixCell{FirstVersion: firstVersion, SecondVersion: secondVersion}
	if detail := unavailableDetail(catalog, input.First.Component, firstVersion); detail != "" {
		cell.Detail = detail
		return cell, nil
	}
	if detail := unavailableDetail(catalog, input.Second.Component, secondVersion); detail != "" {
		cell.Detail = detail
		return cell, nil
	}
	roots := domain.CopyStrings(input.Roots)
	roots[input.First.Component] = firstVersion
	roots[input.Second.Component] = secondVersion
	result, err := session.Solve(ctx, roots)
	if err != nil {
		var fault *domain.Fault
		if errors.As(err, &fault) && fault.Code == "no_solution" {
			cell.Detail = fault.Detail
			cell.Conflicts = fault.Conflicts
			return cell, nil
		}
		return cell, err
	}
	cell.Compatible = true
	cell.Resolved = result.Resolved
	return cell, nil
}

func unavailableDetail(catalog domain.Catalog, id, version string) string {
	release, exists := catalog.Releases[id][version]
	if !exists {
		return fmt.Sprintf("release %s@%s does not exist", id, version)
	}
	if release.State != domain.Available {
		return fmt.Sprintf("release %s@%s is withdrawn", id, version)
	}
	return ""
}

func (s *Service) Matrix(ctx context.Context, id string) (domain.Matrix, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Matrix{}, err
	}
	matrix, exists := state.Matrices[id]
	if !exists {
		return matrix, domain.Missing("matrix", id)
	}
	return matrix, nil
}

func (s *Service) ListMatrices(ctx context.Context) ([]domain.Matrix, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Matrix, 0, len(state.Matrices))
	for _, matrix := range state.Matrices {
		items = append(items, matrix)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}
