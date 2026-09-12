package httpapi

import (
	"net/http"

	"solo-0002-version-compatibility/internal/domain"
)

func (a *API) createDriftCheck(w http.ResponseWriter, r *http.Request) {
	var input domain.DriftCheckInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.CheckEnvironmentDrift(r.Context(), r.PathValue("id"), input)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/drift-checks/"+result.ID)
	respond(w, http.StatusCreated, result)
}

func (a *API) driftCheck(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.DriftCheck(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) driftChecks(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit", "environment_id"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := a.service.ListDriftChecks(r.Context(), r.URL.Query().Get("environment_id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(items, p))
}
