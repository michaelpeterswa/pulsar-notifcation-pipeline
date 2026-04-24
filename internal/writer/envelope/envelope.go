// Package envelope emits the writer's standard {data, meta} JSON success
// envelope (FR-021, SC-013). The envelope is always JSON, always includes
// meta.request_id, and is used across every 2xx response on the writer's HTTP
// API. Errors flow through internal/writer/problems instead (FR-022).
package envelope

import (
	"encoding/json"
	"net/http"
)

// Meta carries the response-scope metadata. Additional fields may be added
// backwards-compatibly via extension; `request_id` is always populated.
type Meta struct {
	RequestID string `json:"request_id"`
}

// Success is the canonical top-level envelope.
type Success[T any] struct {
	Data T    `json:"data"`
	Meta Meta `json:"meta"`
}

// New constructs a Success envelope for payload d correlated with requestID.
func New[T any](requestID string, d T) Success[T] {
	return Success[T]{Data: d, Meta: Meta{RequestID: requestID}}
}

// Write serialises a Success envelope to w with the supplied HTTP status.
// Content-Type is always application/json.
func Write[T any](w http.ResponseWriter, status int, requestID string, data T) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(New(requestID, data))
}
