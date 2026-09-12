package httpapi

import (
	"errors"
	"net/http"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/service"
)

func (a *API) previewImport(w http.ResponseWriter, r *http.Request) {
	var input domain.ImportInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.PreviewImport(r.Context(), input)
	if err != nil {
		fail(w, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
		w.Header().Set("Location", "/api/v1/imports/"+result.Import.ID)
	}
	respond(w, status, map[string]any{
		"import":  result.Import,
		"created": result.Created,
	})
}

func (a *API) imports(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit", "state"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := a.service.ListImports(r.Context(), r.URL.Query().Get("state"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(items, p))
}

func (a *API) importRecord(w http.ResponseWriter, r *http.Request) {
	record, err := a.service.Import(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, record)
}

func (a *API) confirmImport(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	record, err := a.service.ConfirmImport(r.Context(), r.PathValue("id"))
	if err != nil {
		var rejected *service.ImportRejectedError
		if errors.As(err, &rejected) {
			// Whole-batch rejection: no catalog data was created and the
			// import is durably stored as failed, with errors per raw entry.
			respond(w, http.StatusUnprocessableEntity, map[string]any{
				"error": map[string]any{
					"code":   "import_failed",
					"detail": "one or more entries violate identifier, version or dependency rules; nothing was imported",
				},
				"import": rejected.Import,
			})
			return
		}
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, record)
}
