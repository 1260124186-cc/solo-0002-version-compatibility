package httpapi

import (
	"net/http"

	"solo-0002-version-compatibility/internal/domain"
)

func (a *API) createProfile(w http.ResponseWriter, r *http.Request) {
	var input domain.ProfileInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.CreateProfile(r.Context(), input)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/profiles/"+result.ID)
	respond(w, http.StatusCreated, result)
}

func (a *API) profiles(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := a.service.ListProfiles(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(items, p))
}

func (a *API) profile(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.Profile(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) updateProfile(w http.ResponseWriter, r *http.Request) {
	var input domain.ProfileUpdateInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.UpdateProfile(r.Context(), r.PathValue("id"), input)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) deactivateProfile(w http.ResponseWriter, r *http.Request) {
	var input domain.RevisionInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.DeactivateProfile(r.Context(), r.PathValue("id"), input.Revision)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) createEnvironmentFromProfile(w http.ResponseWriter, r *http.Request) {
	var input domain.EnvironmentFromProfileInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.CreateEnvironmentFromProfile(r.Context(), r.PathValue("id"), input)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/environments/"+result.ID)
	respond(w, http.StatusCreated, result)
}
