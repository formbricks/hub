// Package routes contains shared HTTP route registration for API domains.
package routes

import (
	"net/http"

	"github.com/formbricks/hub/internal/api/handlers"
)

type publicTaxonomyHandlerID uint8

const (
	publicListFields publicTaxonomyHandlerID = iota
	publicCreateRun
	publicListRuns
	publicGetActiveTree
	publicGetRun
	publicGetTree
	publicRecordCounts
	publicRenameNode
	publicRemoveNode
	publicListNodeRecords
)

type internalTaxonomyHandlerID uint8

const (
	internalAuthCheck internalTaxonomyHandlerID = iota
	internalGetRunInput
	internalCompleteRun
	internalFailRun
	internalHeartbeat
)

type publicTaxonomyRoute struct {
	method           string
	path             string
	operationID      string
	responseStatuses []string
	handlerID        publicTaxonomyHandlerID
}

type internalTaxonomyRoute struct {
	method    string
	path      string
	handlerID internalTaxonomyHandlerID
}

var publicTaxonomyRoutes = []publicTaxonomyRoute{
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/fields",
		operationID:      "list-taxonomy-fields",
		responseStatuses: []string{"200", "400", "401", "503", "default"},
		handlerID:        publicListFields,
	},
	{
		method:           http.MethodPost,
		path:             "/v1/taxonomy/runs",
		operationID:      "create-taxonomy-run",
		responseStatuses: []string{"200", "202", "400", "401", "409", "503", "default"},
		handlerID:        publicCreateRun,
	},
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/runs",
		operationID:      "list-taxonomy-runs",
		responseStatuses: []string{"200", "400", "401", "default"},
		handlerID:        publicListRuns,
	},
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/runs/active/tree",
		operationID:      "get-active-taxonomy-tree",
		responseStatuses: []string{"200", "400", "401", "404", "default"},
		handlerID:        publicGetActiveTree,
	},
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/runs/{run_id}",
		operationID:      "get-taxonomy-run",
		responseStatuses: []string{"200", "400", "401", "404", "default"},
		handlerID:        publicGetRun,
	},
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/runs/{run_id}/tree",
		operationID:      "get-taxonomy-run-tree",
		responseStatuses: []string{"200", "400", "401", "404", "default"},
		handlerID:        publicGetTree,
	},
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/runs/{run_id}/record-counts",
		operationID:      "count-taxonomy-node-records",
		responseStatuses: []string{"200", "400", "401", "404", "default"},
		handlerID:        publicRecordCounts,
	},
	{
		method:           http.MethodPatch,
		path:             "/v1/taxonomy/nodes/{node_id}",
		operationID:      "rename-taxonomy-node",
		responseStatuses: []string{"200", "400", "401", "404", "409", "default"},
		handlerID:        publicRenameNode,
	},
	{
		method:           http.MethodDelete,
		path:             "/v1/taxonomy/nodes/{node_id}",
		operationID:      "remove-taxonomy-node",
		responseStatuses: []string{"200", "400", "401", "404", "409", "default"},
		handlerID:        publicRemoveNode,
	},
	{
		method:           http.MethodGet,
		path:             "/v1/taxonomy/nodes/{node_id}/records",
		operationID:      "list-taxonomy-node-records",
		responseStatuses: []string{"200", "400", "401", "default"},
		handlerID:        publicListNodeRecords,
	},
}

var internalTaxonomyRoutes = []internalTaxonomyRoute{
	{method: http.MethodGet, path: "/internal/v1/taxonomy/auth-check", handlerID: internalAuthCheck},
	{method: http.MethodGet, path: "/internal/v1/taxonomy/runs/{run_id}/input", handlerID: internalGetRunInput},
	{method: http.MethodPut, path: "/internal/v1/taxonomy/runs/{run_id}/result", handlerID: internalCompleteRun},
	{method: http.MethodPost, path: "/internal/v1/taxonomy/runs/{run_id}/failed", handlerID: internalFailRun},
	{method: http.MethodPost, path: "/internal/v1/taxonomy/runs/{run_id}/heartbeat", handlerID: internalHeartbeat},
}

// RegisterPublicTaxonomy registers the Hub API key-protected taxonomy endpoints.
func RegisterPublicTaxonomy(mux *http.ServeMux, handler *handlers.TaxonomyHandler) {
	for _, route := range publicTaxonomyRoutes {
		mux.HandleFunc(route.method+" "+route.path, publicTaxonomyHandler(handler, route.handlerID))
	}
}

// RegisterInternalTaxonomy registers the internal taxonomy-service endpoints.
func RegisterInternalTaxonomy(mux *http.ServeMux, handler *handlers.TaxonomyInternalHandler) {
	for _, route := range internalTaxonomyRoutes {
		mux.HandleFunc(route.method+" "+route.path, internalTaxonomyHandler(handler, route.handlerID))
	}
}

func publicTaxonomyHandler(handler *handlers.TaxonomyHandler, handlerID publicTaxonomyHandlerID) http.HandlerFunc {
	switch handlerID {
	case publicListFields:
		return handler.ListFields
	case publicCreateRun:
		return handler.CreateRun
	case publicListRuns:
		return handler.ListRuns
	case publicGetActiveTree:
		return handler.GetActiveTree
	case publicGetRun:
		return handler.GetRun
	case publicGetTree:
		return handler.GetTree
	case publicRecordCounts:
		return handler.RecordCounts
	case publicRenameNode:
		return handler.RenameNode
	case publicRemoveNode:
		return handler.RemoveNode
	case publicListNodeRecords:
		return handler.ListNodeRecords
	default:
		panic("unknown public taxonomy handler")
	}
}

func internalTaxonomyHandler(
	handler *handlers.TaxonomyInternalHandler,
	handlerID internalTaxonomyHandlerID,
) http.HandlerFunc {
	switch handlerID {
	case internalAuthCheck:
		return handler.AuthCheck
	case internalGetRunInput:
		return handler.GetRunInput
	case internalCompleteRun:
		return handler.CompleteRun
	case internalFailRun:
		return handler.FailRun
	case internalHeartbeat:
		return handler.Heartbeat
	default:
		panic("unknown internal taxonomy handler")
	}
}
