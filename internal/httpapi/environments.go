package httpapi

import (
	"net/http"

	"solo-0002-version-compatibility/internal/domain"
)

func (a *API) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var input domain.EnvironmentInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.CreateEnvironment(r.Context(), input)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/environments/"+result.ID)
	respond(w, http.StatusCreated, result)
}

func (a *API) environments(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := a.service.ListEnvironments(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(items, p))
}

func (a *API) environment(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.Environment(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) rootTimeline(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit", "plan_id"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.RootTimeline(r.Context(), r.PathValue("id"), r.URL.Query().Get("plan_id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(result.Entries, p))
}
