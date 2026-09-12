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
	if err := queryOnly(r, "offset", "limit", "state", "constraint"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	state, constraint, err := releaseFilters(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := a.service.ListReleases(r.Context(), r.PathValue("id"), state, constraint)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(items, p))
}

// releaseFilters reads the optional state and constraint filters. A filter
// that is present must carry a value; an empty one is never silently dropped.
func releaseFilters(r *http.Request) (string, string, error) {
	query := r.URL.Query()
	var state, constraint string
	if values, present := query["state"]; present {
		if values[0] == "" {
			return "", "", domain.Invalid("state filter must not be empty")
		}
		state = values[0]
	}
	if values, present := query["constraint"]; present {
		if values[0] == "" {
			return "", "", domain.Invalid("constraint filter must not be empty")
		}
		constraint = values[0]
	}
	return state, constraint, nil
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
