package httpapi

import (
	"net/http"
	"strconv"

	"solo-0002-version-compatibility/internal/domain"
)

type pagination struct{ Offset, Limit int }

func pageRequest(r *http.Request) (pagination, error) {
	p := pagination{Limit: 50}
	for name, target := range map[string]*int{"offset": &p.Offset, "limit": &p.Limit} {
		if raw := r.URL.Query().Get(name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 {
				return p, domain.Invalid("%s must be a nonnegative integer", name)
			}
			*target = n
		}
	}
	if p.Limit < 1 || p.Limit > 200 {
		return p, domain.Invalid("limit must be between 1 and 200")
	}
	return p, nil
}

func pageOf[T any](items []T, p pagination) map[string]any {
	start := p.Offset
	if start > len(items) {
		start = len(items)
	}
	end := len(items)
	if p.Limit < end-start {
		end = start + p.Limit
	}
	return map[string]any{"items": items[start:end], "total": len(items), "offset": p.Offset, "limit": p.Limit}
}

func queryOnly(r *http.Request, names ...string) error {
	allowed := make(map[string]bool)
	for _, name := range names {
		allowed[name] = true
	}
	for key, values := range r.URL.Query() {
		if !allowed[key] || len(values) != 1 {
			return domain.Invalid("unknown or repeated query parameter %q", key)
		}
	}
	return nil
}
