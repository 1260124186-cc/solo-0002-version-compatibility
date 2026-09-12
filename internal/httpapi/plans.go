package httpapi

import (
	"net/http"
	"strconv"

	"solo-0002-version-compatibility/internal/domain"
)

func (a *API) createPlan(w http.ResponseWriter, r *http.Request) {
	var input domain.PlanInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.CreatePlan(r.Context(), input)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/plans/"+result.ID)
	respond(w, http.StatusCreated, result)
}

func (a *API) plans(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit", "environment_id", "state"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := a.service.ListPlans(r.Context(), r.URL.Query().Get("environment_id"), r.URL.Query().Get("state"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(items, p))
}

func (a *API) plan(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.Plan(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) validatePlan(w http.ResponseWriter, r *http.Request) {
	var input domain.RevisionInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.ValidatePlan(r.Context(), r.PathValue("id"), input.Revision)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) applyPlan(w http.ResponseWriter, r *http.Request) {
	var input domain.RevisionInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.ApplyPlan(r.Context(), r.PathValue("id"), input.Revision)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) cancelPlan(w http.ResponseWriter, r *http.Request) {
	var input domain.ReasonInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.CancelPlan(r.Context(), r.PathValue("id"), input)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) correctPlan(w http.ResponseWriter, r *http.Request) {
	var input domain.ReasonInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.CorrectPlan(r.Context(), r.PathValue("id"), input)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "after", "limit", "entity_id"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	var after uint64
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseUint(raw, 10, 64)
		if err != nil {
			fail(w, domain.Invalid("after must be a nonnegative integer"))
			return
		}
	}
	result, err := a.service.Events(r.Context(), after, p.Limit, r.URL.Query().Get("entity_id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}
