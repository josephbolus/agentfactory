package controlplane

import (
	"net/http"
)

func (a *API) listExecutionProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := a.store.ExecutionProfiles(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

func (a *API) getExecutionProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := a.store.ExecutionProfile(r.Context(), r.PathValue("profile_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}
