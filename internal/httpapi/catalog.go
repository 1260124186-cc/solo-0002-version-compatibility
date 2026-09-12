package httpapi

import (
	"net/http"

	"solo-0002-version-compatibility/internal/domain"
)

func (a *API) createComponent(w http.ResponseWriter, r *http.Request) {
	var input domain.ComponentInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.CreateComponent(r.Context(), input)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/components/"+result.ID)
	respond(w, http.StatusCreated, result)
}

func (a *API) components(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, revision, err := a.service.ListComponents(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	result := pageOf(items, p)
	result["catalog_revision"] = revision
	respond(w, http.StatusOK, result)
}

func (a *API) component(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.Component(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) addRelease(w http.ResponseWriter, r *http.Request) {
	var input domain.ReleaseInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.AddRelease(r.Context(), r.PathValue("id"), input)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusCreated, result)
}

func (a *API) releases(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit", "channel"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := a.service.ListReleases(r.Context(), r.PathValue("id"), r.URL.Query().Get("channel"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(items, p))
}

func (a *API) withdraw(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.WithdrawRelease(r.Context(), r.PathValue("id"), r.PathValue("version"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) resolve(w http.ResponseWriter, r *http.Request) {
	var input domain.ResolutionInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.Resolve(r.Context(), input)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}
