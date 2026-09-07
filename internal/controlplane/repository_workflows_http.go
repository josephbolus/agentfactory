package controlplane

import (
	"net/http"

	"github.com/jbolus-owens/factory/internal/protocol"
)

func (a *API) listRepositoryWorkflows(w http.ResponseWriter, r *http.Request) {
	catalog, err := a.store.WorkflowCatalog(r.Context(), r.PathValue("repository_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (a *API) refreshRepositoryWorkflows(w http.ResponseWriter, r *http.Request) {
	if !prepareMutation(w, r, protocol.MaxBodyBytes) || !decodeEmptyJSON(w, r) {
		return
	}
	catalog, err := a.store.RefreshRepositoryWorkflowCatalog(r.Context(), r.PathValue("repository_id"))
	if err != nil && catalog.Status == "" {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (a *API) workflowOptions(w http.ResponseWriter, r *http.Request) {
	if !prepareMutation(w, r, protocol.MaxBodyBytes) {
		return
	}
	var input protocol.RepositoryWorkflowOptionsRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	options, err := a.store.WorkflowOptions(r.Context(), input.RepositoryIDs)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, protocol.RepositoryWorkflowOptionsResponse{Workflows: options})
}
