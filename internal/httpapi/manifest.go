package httpapi

import (
	"net/http"

	"solo-0002-version-compatibility/internal/domain"
)

// maxManifestBodyBytes gives manifests room for the largest resolvable set
// (128 components with dependency definitions) while staying bounded.
const maxManifestBodyBytes = 1 << 20

func (a *API) environmentManifest(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.EnvironmentManifest(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) planManifest(w http.ResponseWriter, r *http.Request) {
	result, err := a.service.PlanManifest(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a *API) verifyManifest(w http.ResponseWriter, r *http.Request) {
	var m domain.Manifest
	if err := decodeLimit(w, r, &m, maxManifestBodyBytes); err != nil {
		fail(w, err)
		return
	}
	report, err := a.service.VerifyManifest(r.Context(), m)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, report)
}
