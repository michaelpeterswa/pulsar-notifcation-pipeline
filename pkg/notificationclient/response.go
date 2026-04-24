package notificationclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SubmitResponse is the decoded {data, meta} success envelope. Use the
// accessor methods to read fields — they provide typed values and keep the
// caller away from the generated JSON-shape structs.
type SubmitResponse struct {
	data submitData
	meta metaShape
}

type submitData struct {
	NotificationID string `json:"notification_id"`
	AcceptedAt     string `json:"accepted_at"`
}

type metaShape struct {
	RequestID string `json:"request_id"`
}

type envelope struct {
	Data submitData `json:"data"`
	Meta metaShape  `json:"meta"`
}

// NotificationID returns the pipeline-assigned identifier for the submitted
// notification.
func (r *SubmitResponse) NotificationID() string { return r.data.NotificationID }

// AcceptedAt returns the server-stamped acceptance timestamp. Returns the
// zero value if the server omitted or malformed it.
func (r *SubmitResponse) AcceptedAt() time.Time {
	if r.data.AcceptedAt == "" {
		return time.Time{}
	}
	// data-model.md §6.1 uses "2006-01-02T15:04:05.000Z07:00"; be permissive.
	for _, layout := range []string{
		"2006-01-02T15:04:05.000Z07:00",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, r.data.AcceptedAt); err == nil {
			return t
		}
	}
	return time.Time{}
}

// RequestID returns the server-generated request-scope correlation ID
// (meta.request_id).
func (r *SubmitResponse) RequestID() string { return r.meta.RequestID }

// parseResponse decodes an HTTP response into either a *SubmitResponse
// (on 2xx) or a *ProblemError (on anything else).
func parseResponse(resp *http.Response) (*SubmitResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("notificationclient: read response: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("notificationclient: decode success envelope: %w", err)
		}
		return &SubmitResponse{data: env.Data, meta: env.Meta}, nil
	}
	return nil, decodeProblem(body, resp.StatusCode)
}
