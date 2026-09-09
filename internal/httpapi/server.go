package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"solo-0002-version-compatibility/internal/service"
)

type API struct {
	service  *service.Service
	logger   *slog.Logger
	timeout  time.Duration
	requests atomic.Uint64
	capacity chan struct{}
}

func New(s *service.Service, logger *slog.Logger, timeout time.Duration) http.Handler {
	a := &API{service: s, logger: logger, timeout: timeout, capacity: make(chan struct{}, 32)}
	mux := http.NewServeMux()
	a.route(mux, "/healthz", map[string]http.HandlerFunc{"GET": a.health})
	a.route(mux, "/api/v1/components", map[string]http.HandlerFunc{"GET": a.components, "POST": a.createComponent})
	a.route(mux, "/api/v1/components/{id}", map[string]http.HandlerFunc{"GET": a.component})
	a.route(mux, "/api/v1/components/{id}/releases", map[string]http.HandlerFunc{"GET": a.releases, "POST": a.addRelease})
	a.route(mux, "/api/v1/components/{id}/releases/{version}/withdraw", map[string]http.HandlerFunc{"POST": a.withdraw})
	a.route(mux, "/api/v1/resolve", map[string]http.HandlerFunc{"POST": a.resolve})
	a.route(mux, "/api/v1/environments", map[string]http.HandlerFunc{"GET": a.environments, "POST": a.createEnvironment})
	a.route(mux, "/api/v1/environments/{id}", map[string]http.HandlerFunc{"GET": a.environment})
	a.route(mux, "/api/v1/plans", map[string]http.HandlerFunc{"GET": a.plans, "POST": a.createPlan})
	a.route(mux, "/api/v1/plans/{id}", map[string]http.HandlerFunc{"GET": a.plan})
	a.route(mux, "/api/v1/plans/{id}/validate", map[string]http.HandlerFunc{"POST": a.validatePlan})
	a.route(mux, "/api/v1/plans/{id}/apply", map[string]http.HandlerFunc{"POST": a.applyPlan})
	a.route(mux, "/api/v1/plans/{id}/cancel", map[string]http.HandlerFunc{"POST": a.cancelPlan})
	a.route(mux, "/api/v1/events", map[string]http.HandlerFunc{"GET": a.events})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "not_found", "detail": "unknown endpoint"}})
	})
	return a.wrap(mux)
}

func (a *API) route(mux *http.ServeMux, path string, methods map[string]http.HandlerFunc) {
	allow := make([]string, 0, len(methods))
	for method := range methods {
		allow = append(allow, method)
	}
	sort.Strings(allow)
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		handler, exists := methods[r.Method]
		if !exists {
			w.Header().Set("Allow", strings.Join(allow, ", "))
			respond(w, http.StatusMethodNotAllowed, map[string]any{"error": map[string]string{"code": "method_not_allowed", "detail": "unsupported HTTP method"}})
			return
		}
		handler(w, r)
	})
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, map[string]any{"status": "ok", "service": "version-compatibility", "api_version": 1})
}

func (a *API) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		id := fmt.Sprintf("compat-%d", a.requests.Add(1))
		w.Header().Set("X-Request-ID", id)
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("request panic", "request_id", id, "cause", recovered)
				fail(w, fmt.Errorf("request panic"))
			}
			a.logger.Info("HTTP request", "request_id", id, "method", r.Method, "path", r.URL.Path, "elapsed", time.Since(started))
		}()
		select {
		case a.capacity <- struct{}{}:
			defer func() { <-a.capacity }()
		default:
			w.Header().Set("Retry-After", "1")
			respond(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "busy", "detail": "service concurrency limit reached"}})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), a.timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
