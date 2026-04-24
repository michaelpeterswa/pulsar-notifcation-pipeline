// Package pushover implements provider.PushProvider against the Pushover
// Messages API (https://pushover.net/api). It is a thin HTTP wrapper — the
// constitution's dependency-hygiene clause makes a full SDK unnecessary for a
// one-endpoint service.
//
// Classification policy (research.md §6):
//
//	2xx, status:1 → delivered
//	400 with user/token errors → permanent, provider-rejected-recipient
//	400 other → permanent, provider-rejected-payload
//	401/403 → permanent, provider-auth-failed
//	429, 5xx, network error → transient
package pushover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

const endpointPath = "/1/messages.json"

// Provider implements provider.PushProvider for Pushover.
type Provider struct {
	appToken string
	baseURL  string
	httpc    *http.Client
}

// Option is the functional option for New.
type Option func(*Provider)

// WithHTTPClient overrides the HTTP client. Default: http.Client with a 10s timeout.
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpc = c } }

// WithBaseURL overrides the API base URL (for testing against httptest).
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = strings.TrimRight(u, "/") } }

// WithAppToken supplies the Pushover application API token. Required.
func WithAppToken(t string) Option { return func(p *Provider) { p.appToken = t } }

// New constructs a Pushover provider.
func New(opts ...Option) (*Provider, error) {
	p := &Provider{
		baseURL: "https://api.pushover.net",
		httpc:   &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.appToken == "" {
		return nil, errors.New("pushover: WithAppToken is required")
	}
	return p, nil
}

// Name implements provider.PushProvider.
func (p *Provider) Name() string { return "pushover" }

// Push implements provider.PushProvider.
func (p *Provider) Push(ctx context.Context, n *notificationpbv1.Notification) (*provider.Result, error) {
	target, ok := n.GetTarget().(*notificationpbv1.Notification_Pushover)
	if !ok || target == nil || target.Pushover == nil {
		return nil, &provider.PermanentError{Reason: "provider-rejected-payload", Err: errors.New("pushover: missing PushoverTarget")}
	}
	t := target.Pushover
	c := n.GetContent()
	if c == nil {
		return nil, &provider.PermanentError{Reason: "provider-rejected-payload", Err: errors.New("pushover: missing Content")}
	}

	form := url.Values{}
	form.Set("token", p.appToken)
	form.Set("user", t.GetUserOrGroupKey())
	form.Set("title", c.GetTitle())
	form.Set("message", c.GetBody())
	if pr := c.GetPriority(); pr != 0 {
		form.Set("priority", strconv.Itoa(int(pr)))
	}
	if d := t.GetDeviceName(); d != "" {
		form.Set("device", d)
	}
	if s := t.GetSound(); s != "" {
		form.Set("sound", s)
	}
	if u := t.GetUrl(); u != "" {
		form.Set("url", u)
	}
	if ut := t.GetUrlTitle(); ut != "" {
		form.Set("url_title", ut)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+endpointPath, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, &provider.TransientError{Err: fmt.Errorf("pushover: build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpc.Do(req)
	if err != nil {
		return nil, &provider.TransientError{Err: fmt.Errorf("pushover: http do: %w", err)}
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	snippet := truncate(string(bodyBytes), 512)

	return classify(resp.StatusCode, bodyBytes, snippet)
}

// pushoverResp mirrors the JSON response shape of /1/messages.json.
type pushoverResp struct {
	Status int      `json:"status"`
	Errors []string `json:"errors,omitempty"`
	Info   string   `json:"info,omitempty"`
}

func classify(status int, body []byte, snippet string) (*provider.Result, error) {
	var pr pushoverResp
	_ = json.Unmarshal(body, &pr)

	switch {
	case status >= 200 && status < 300 && pr.Status == 1:
		return &provider.Result{ProviderResponse: snippet}, nil
	case status == 400:
		if isRecipientRejection(pr.Errors) {
			return nil, &provider.PermanentError{
				Reason:   "provider-rejected-recipient",
				Response: snippet,
				Err:      errors.New("pushover: recipient rejected"),
			}
		}
		return nil, &provider.PermanentError{
			Reason:   "provider-rejected-payload",
			Response: snippet,
			Err:      errors.New("pushover: payload rejected"),
		}
	case status == 401 || status == 403:
		return nil, &provider.PermanentError{
			Reason:   "provider-auth-failed",
			Response: snippet,
			Err:      errors.New("pushover: auth failed (check PUSHOVER_APP_TOKEN)"),
		}
	case status == 429, status >= 500:
		return nil, &provider.TransientError{Response: snippet, Err: fmt.Errorf("pushover: %d", status)}
	default:
		// Unexpected statuses treated as transient (conservative).
		return nil, &provider.TransientError{Response: snippet, Err: fmt.Errorf("pushover: unexpected status %d", status)}
	}
}

func isRecipientRejection(errs []string) bool {
	for _, e := range errs {
		le := strings.ToLower(e)
		if strings.Contains(le, "user") || strings.Contains(le, "token") || strings.Contains(le, "device") {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
