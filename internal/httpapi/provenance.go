package httpapi

import (
	"net/http"
	"strconv"

	"solo-0002-version-compatibility/internal/domain"
)

func (a *API) provenance(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := a.service.ListProvenance(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(items, p))
}

func (a *API) provenanceRevision(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.ParseUint(r.PathValue("revision"), 10, 64)
	if err != nil || revision == 0 {
		fail(w, domain.Invalid("revision must be a positive integer"))
		return
	}
	result, err := a.service.Provenance(r.Context(), r.PathValue("id"), revision)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}
