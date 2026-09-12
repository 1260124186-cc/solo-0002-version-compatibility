package httpapi

import (
	"net/http"
	"strconv"

	"solo-0002-version-compatibility/internal/domain"
)

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

func (a *API) replayEvents(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "sequence"); err != nil {
		fail(w, err)
		return
	}
	raw := r.URL.Query().Get("sequence")
	if raw == "" {
		fail(w, domain.Invalid("sequence is required"))
		return
	}
	sequence, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		fail(w, domain.Invalid("sequence must be a nonnegative integer"))
		return
	}
	report, err := a.service.ReplayEvents(r.Context(), sequence)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, report)
}
