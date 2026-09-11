package httpapi

import (
	"net/http"
	"strconv"

	"solo-0002-version-compatibility/internal/domain"
)

func (a *API) snapshots(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := a.service.ListSnapshots(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(items, p))
}

func (a *API) snapshot(w http.ResponseWriter, r *http.Request) {
	revision, err := pathRevision(r)
	if err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.Snapshot(r.Context(), r.PathValue("id"), revision)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) verifySnapshot(w http.ResponseWriter, r *http.Request) {
	revision, err := pathRevision(r)
	if err != nil {
		fail(w, err)
		return
	}
	var input domain.VerifySnapshotInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.VerifySnapshot(r.Context(), r.PathValue("id"), revision, input.CatalogRevision)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func pathRevision(r *http.Request) (uint64, error) {
	revision, err := strconv.ParseUint(r.PathValue("revision"), 10, 64)
	if err != nil || revision == 0 {
		return 0, domain.Invalid("revision must be a positive integer")
	}
	return revision, nil
}
