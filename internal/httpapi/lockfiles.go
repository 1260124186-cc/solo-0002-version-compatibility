package httpapi

import (
	"fmt"
	"net/http"

	"solo-0002-version-compatibility/internal/domain"
)

func (a *API) createLockfile(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.CreateLockfile(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/lockfiles/"+result.ID)
	respond(w, http.StatusCreated, result)
}

func (a *API) lockfiles(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit", "environment_id"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := a.service.ListLockfiles(r.Context(), r.URL.Query().Get("environment_id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(items, p))
}

func (a *API) lockfile(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.Lockfile(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) verifyStoredLockfile(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.VerifyStoredLockfile(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) exportLockfile(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.ExportLockfile(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	// Export is a standalone, portable artifact: suggest a deterministic
	// filename while still serving the canonical JSON document.
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="lockfile-%s-rev%d.json"`, result.EnvironmentID, result.EnvironmentRevision))
	respond(w, http.StatusOK, result)
}

func (a *API) verifyLockfileDocument(w http.ResponseWriter, r *http.Request) {
	var lock domain.Lockfile
	if err := decode(w, r, &lock); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.VerifyExportedLockfile(r.Context(), lock)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}
