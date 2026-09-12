package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/service"
)

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "after", "limit", "entity_id", "entity_type", "action", "start_time", "end_time"); err != nil {
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
	filter := service.EventFilter{
		EntityID: r.URL.Query().Get("entity_id"),
		Kind:     r.URL.Query().Get("entity_type"),
		Action:   r.URL.Query().Get("action"),
	}
	query := r.URL.Query()
	if raw := query.Get("start_time"); raw != "" {
		// RFC3339Nano parsing also accepts whole-second timestamps; the event
		// "at" field is emitted with nanosecond precision and may be echoed back.
		filter.Start, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			fail(w, domain.Invalid("start_time must be an RFC 3339 timestamp, e.g. 2026-01-02T15:04:05Z"))
			return
		}
		filter.HasStart = true
	}
	if raw := query.Get("end_time"); raw != "" {
		filter.End, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			fail(w, domain.Invalid("end_time must be an RFC 3339 timestamp, e.g. 2026-01-02T15:04:05Z"))
			return
		}
		filter.HasEnd = true
	}
	if filter.HasStart && filter.HasEnd && filter.Start.After(filter.End) {
		fail(w, domain.Invalid("start_time must not be later than end_time"))
		return
	}
	result, err := a.service.Events(r.Context(), after, p.Limit, filter)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}
