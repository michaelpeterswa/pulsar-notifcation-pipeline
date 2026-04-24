// Package notificationclient is the importable Go HTTP client for the
// pulsar-notifcation-pipeline's writer API. Upstream Go services import this
// package to submit notifications without hand-rolling HTTP calls.
//
// Generated from api/openapi.yaml via oapi-codegen; this file wraps the
// generated client with a functional-options constructor and typed
// convenience accessors (FR-019, FR-024, SC-012).
//
//	c, _ := notificationclient.New("https://writer.example",
//	    notificationclient.WithBearerToken("abc"))
//	resp, err := c.Submit(ctx, notificationclient.NewPushoverRequest(
//	    "uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "Title", "Body"))
//	if err != nil { /* ... */ }
//	log.Println(resp.NotificationID(), resp.RequestID())
package notificationclient

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationclient/generated"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

// Client submits notifications to the writer's HTTP API.
type Client struct {
	raw         *generated.Client
	bearerToken string
	userAgent   string
	contentType string
}

// Option is the functional option for New.
type Option func(*Client) error

// WithBearerToken attaches a bearer token to every outbound request.
func WithBearerToken(token string) Option {
	return func(c *Client) error { c.bearerToken = token; return nil }
}

// WithHTTPClient overrides the underlying http.Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) error {
		return generated.WithHTTPClient(h)(c.raw)
	}
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) error { c.userAgent = ua; return nil }
}

// WithContentType selects the content type used for request bodies. Accepts
// "application/json" (default) or "application/x-protobuf".
func WithContentType(ct string) Option {
	return func(c *Client) error { c.contentType = ct; return nil }
}

// ContentType values accepted by WithContentType.
const (
	ContentTypeJSON     = "application/json"
	ContentTypeProtobuf = "application/x-protobuf"
)

// New constructs a Client targeting the writer's base URL.
func New(baseURL string, opts ...Option) (*Client, error) {
	if baseURL == "" {
		return nil, errors.New("notificationclient: baseURL is required")
	}
	raw, err := generated.NewClient(baseURL)
	if err != nil {
		return nil, err
	}
	c := &Client{
		raw:         raw,
		userAgent:   "pulsar-notifcation-pipeline-client/0.1",
		contentType: ContentTypeJSON,
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// SubmitRequest is the high-level request shape. Build via NewPushoverRequest
// (or future helpers per provider).
type SubmitRequest struct {
	// Notification is the canonical protobuf message. Server-assigned fields
	// (notification_id, created_at, originating_service) are ignored and
	// overwritten by the writer.
	Notification *notificationpbv1.Notification
	// IdempotencyKey, if non-empty, is forwarded as the Idempotency-Key
	// header. The canonical protobuf's idempotency_key field is also filled
	// for consistency.
	IdempotencyKey string
}

// NewPushoverRequest is a convenience constructor for the MVP's single
// push provider.
func NewPushoverRequest(userOrGroupKey, title, body string) *SubmitRequest {
	return &SubmitRequest{
		Notification: &notificationpbv1.Notification{
			Content: &notificationpbv1.Content{Title: title, Body: body},
			Target: &notificationpbv1.Notification_Pushover{
				Pushover: &notificationpbv1.PushoverTarget{UserOrGroupKey: userOrGroupKey},
			},
		},
	}
}

// Submit sends the request. On any non-2xx response, the returned error is a
// *ProblemError wrapping the RFC 9457 body; errors.Is against the exported
// sentinels in this package identifies the category.
func (c *Client) Submit(ctx context.Context, req *SubmitRequest) (*SubmitResponse, error) {
	if req == nil || req.Notification == nil {
		return nil, errors.New("notificationclient: nil request")
	}
	if req.IdempotencyKey != "" {
		req.Notification.IdempotencyKey = req.IdempotencyKey
	}

	var bodyBytes []byte
	var err error
	switch c.contentType {
	case ContentTypeJSON, "":
		bodyBytes, err = protojson.Marshal(req.Notification)
	case ContentTypeProtobuf:
		bodyBytes, err = proto.Marshal(req.Notification)
	default:
		return nil, errors.New("notificationclient: unsupported content type: " + c.contentType)
	}
	if err != nil {
		return nil, err
	}

	params := &generated.SubmitNotificationParams{}
	if req.IdempotencyKey != "" {
		k := req.IdempotencyKey
		params.IdempotencyKey = &k
	}
	editor := func(_ context.Context, r *http.Request) error {
		if c.bearerToken != "" {
			r.Header.Set("Authorization", "Bearer "+c.bearerToken)
		}
		if c.userAgent != "" {
			r.Header.Set("User-Agent", c.userAgent)
		}
		return nil
	}
	ct := c.contentType
	if ct == "" {
		ct = ContentTypeJSON
	}
	resp, err := c.raw.SubmitNotificationWithBody(ctx, params, ct, bytes.NewReader(bodyBytes), editor)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseResponse(resp)
}
