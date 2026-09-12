package httpapi

import (
	"net/http"

	"solo-0002-version-compatibility/internal/domain"
)

func (a *API) createChangeSet(w http.ResponseWriter, r *http.Request) {
	var input domain.ChangeSetInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.CreateChangeSet(r.Context(), input)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/change-sets/"+result.ID)
	respond(w, http.StatusCreated, result)
}

func (a *API) changeSets(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit", "state"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := a.service.ListChangeSets(r.Context(), r.URL.Query().Get("state"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(items, p))
}

func (a *API) changeSet(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.ChangeSet(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) validateChangeSet(w http.ResponseWriter, r *http.Request) {
	var input domain.RevisionInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.ValidateChangeSet(r.Context(), r.PathValue("id"), input.Revision)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) applyChangeSet(w http.ResponseWriter, r *http.Request) {
	var input domain.RevisionInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.ApplyChangeSet(r.Context(), r.PathValue("id"), input.Revision)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) cancelChangeSet(w http.ResponseWriter, r *http.Request) {
	var input domain.RevisionInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.CancelChangeSet(r.Context(), r.PathValue("id"), input.Revision)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}
