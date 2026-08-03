// Package routes contains shared HTTP route registration for API domains.
package routes

import (
	"net/http"

	"github.com/formbricks/hub/internal/api/handlers"
)

type publicTaxonomyRoute struct {
	method           string
	path             string
	operationID      string
	responseStatuses []string
	handler          func(*handlers.TaxonomyHandler, http.ResponseWriter, *http.Request)
}

type internalTaxonomyRoute struct {
	method  string
	path    string
	handler func(*handlers.TaxonomyInternalHandler, http.ResponseWriter, *http.Request)
}

var publicTaxonomyRoutes = []publicTaxonomyRoute{
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/fields",
		operationID:      "list-taxonomy-fields",
		responseStatuses: []string{"200", "400", "401", "503", "default"},
		handler:          (*handlers.TaxonomyHandler).ListFields,
	},
	{
		method:           http.MethodPost,
		path:             "/v1/taxonomy/runs",
		operationID:      "create-taxonomy-run",
		responseStatuses: []string{"200", "202", "400", "401", "409", "503", "default"},
		handler:          (*handlers.TaxonomyHandler).CreateRun,
	},
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/runs",
		operationID:      "list-taxonomy-runs",
		responseStatuses: []string{"200", "400", "401", "default"},
		handler:          (*handlers.TaxonomyHandler).ListRuns,
	},
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/runs/active/tree",
		operationID:      "get-active-taxonomy-tree",
		responseStatuses: []string{"200", "400", "401", "404", "default"},
		handler:          (*handlers.TaxonomyHandler).GetActiveTree,
	},
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/runs/{run_id}",
		operationID:      "get-taxonomy-run",
		responseStatuses: []string{"200", "400", "401", "404", "default"},
		handler:          (*handlers.TaxonomyHandler).GetRun,
	},
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/runs/{run_id}/tree",
		operationID:      "get-taxonomy-run-tree",
		responseStatuses: []string{"200", "400", "401", "404", "default"},
		handler:          (*handlers.TaxonomyHandler).GetTree,
	},
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/runs/{run_id}/record-counts",
		operationID:      "count-taxonomy-node-records",
		responseStatuses: []string{"200", "400", "401", "404", "default"},
		handler:          (*handlers.TaxonomyHandler).RecordCounts,
	},
	{
		method:           http.MethodPatch,
		path:             "/v1/taxonomy/nodes/{node_id}",
		operationID:      "rename-taxonomy-node",
		responseStatuses: []string{"200", "400", "401", "404", "409", "default"},
		handler:          (*handlers.TaxonomyHandler).RenameNode,
	},
	{
		method:           http.MethodDelete,
		path:             "/v1/taxonomy/nodes/{node_id}",
		operationID:      "remove-taxonomy-node",
		responseStatuses: []string{"200", "400", "401", "404", "409", "default"},
		handler:          (*handlers.TaxonomyHandler).RemoveNode,
	},
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/nodes/{node_id}/records",
		operationID:      "list-taxonomy-node-records",
		responseStatuses: []string{"200", "400", "401", "default"},
		handler:          (*handlers.TaxonomyHandler).ListNodeRecords,
	},
}

var internalTaxonomyRoutes = []internalTaxonomyRoute{
	{
		method:  http.MethodGet,
		path:    "/internal/v1/taxonomy/auth-check",
		handler: (*handlers.TaxonomyInternalHandler).AuthCheck,
	},
	{
		method:  http.MethodGet,
		path:    "/internal/v1/taxonomy/runs/{run_id}/input",
		handler: (*handlers.TaxonomyInternalHandler).GetRunInput,
	},
	{
		method:  http.MethodPut,
		path:    "/internal/v1/taxonomy/runs/{run_id}/result",
		handler: (*handlers.TaxonomyInternalHandler).CompleteRun,
	},
	{
		method:  http.MethodPost,
		path:    "/internal/v1/taxonomy/runs/{run_id}/failed",
		handler: (*handlers.TaxonomyInternalHandler).FailRun,
	},
	{
		method:  http.MethodPost,
		path:    "/internal/v1/taxonomy/runs/{run_id}/heartbeat",
		handler: (*handlers.TaxonomyInternalHandler).Heartbeat,
	},
}

// RegisterPublicTaxonomy registers the Hub API key-protected taxonomy endpoints.
func RegisterPublicTaxonomy(mux *http.ServeMux, handler *handlers.TaxonomyHandler) {
	for _, route := range publicTaxonomyRoutes {
		mux.HandleFunc(route.method+" "+route.path, func(w http.ResponseWriter, r *http.Request) {
			route.handler(handler, w, r)
		})
	}
}

// RegisterInternalTaxonomy registers the internal taxonomy-service endpoints.
func RegisterInternalTaxonomy(mux *http.ServeMux, handler *handlers.TaxonomyInternalHandler) {
	for _, route := range internalTaxonomyRoutes {
		mux.HandleFunc(route.method+" "+route.path, func(w http.ResponseWriter, r *http.Request) {
			route.handler(handler, w, r)
		})
	}
}
