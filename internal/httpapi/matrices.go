package httpapi

import (
	"net/http"

	"solo-0002-version-compatibility/internal/domain"
)

func (a *API) createMatrix(w http.ResponseWriter, r *http.Request) {
	var input domain.MatrixInput
	if err := decode(w, r, &input); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.ComputeMatrix(r.Context(), input)
	if err != nil {
		fail(w, err)
		return
	}
	if result.Saved {
		w.Header().Set("Location", "/api/v1/matrices/"+result.ID)
		respond(w, http.StatusCreated, result)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) matrices(w http.ResponseWriter, r *http.Request) {
	if err := queryOnly(r, "offset", "limit"); err != nil {
		fail(w, err)
		return
	}
	p, err := pageRequest(r)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := a.service.ListMatrices(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, pageOf(items, p))
}

func (a *API) matrix(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.Matrix(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}
