package httpserver

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/metrics"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/auth"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/envelope"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/ingest"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/problems"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

const (
	contentTypeJSON        = "application/json"
	contentTypeProtobuf    = "application/x-protobuf"
	contentTypeProtobufAlt = "application/protobuf"
	maxRequestBodyBytes    = 1 << 20 // 1 MiB
)

// Handlers implements the generated ServerInterface. Constructed via
// functional options.
type Handlers struct {
	ingester  *ingest.Ingester
	requestID func(r *http.Request) string
}

// HandlerOption is the functional option for NewHandlers.
type HandlerOption func(*Handlers)

// WithIngester supplies the ingest engine. Required.
func WithIngester(i *ingest.Ingester) HandlerOption { return func(h *Handlers) { h.ingester = i } }

// WithRequestIDExtractor supplies the request-ID extractor used on every
// response envelope. Required for SC-005 correlation; supply a function that
// pulls the ID the router's middleware attached to the context.
func WithRequestIDExtractor(fn func(r *http.Request) string) HandlerOption {
	return func(h *Handlers) { h.requestID = fn }
}

// NewHandlers constructs a Handlers.
func NewHandlers(opts ...HandlerOption) (*Handlers, error) {
	h := &Handlers{}
	for _, opt := range opts {
		opt(h)
	}
	if h.ingester == nil {
		return nil, errors.New("httpserver: WithIngester is required")
	}
	if h.requestID == nil {
		h.requestID = func(r *http.Request) string { return r.Header.Get("X-Request-Id") }
	}
	return h, nil
}

// SubmitNotification implements ServerInterface.
func (h *Handlers) SubmitNotification(w http.ResponseWriter, r *http.Request, params SubmitNotificationParams) {
	start := time.Now()
	rid := h.requestID(r)
	outcome := metrics.OutcomeInternal
	serviceName := ""
	defer func() {
		metrics.RecordWriterSubmission(r.Context(), outcome, serviceName, time.Since(start).Seconds())
	}()

	// Parse the body per the Content-Type. Generated types from oapi-codegen
	// describe the JSON shape; we decode both content types directly into the
	// canonical protobuf.
	n, perr := decodeBody(r, rid)
	if perr != nil {
		if perr.Status == http.StatusUnsupportedMediaType {
			outcome = metrics.OutcomeValidationFailed
		} else {
			outcome = metrics.OutcomeValidationFailed
		}
		problems.Write(w, perr)
		return
	}

	// Retrieve the authenticated principal (auth middleware populates this).
	p, ok := auth.FromContext(r.Context())
	if !ok {
		outcome = metrics.OutcomeAuthFailed
		problems.Write(w, problems.AuthenticationRequired(rid, r.URL.Path))
		return
	}
	serviceName = p.ServiceName

	// Thread the optional idempotency-key header into the canonical message.
	if params.IdempotencyKey != nil {
		n.IdempotencyKey = *params.IdempotencyKey
	}

	res, err := h.ingester.Accept(r.Context(), n, p.ServiceName)
	if err != nil {
		outcome = h.outcomeForIngestError(err)
		h.writeIngestError(w, r, rid, err)
		return
	}

	outcome = metrics.OutcomeAccepted
	envelope.Write(w, http.StatusAccepted, rid, submitData{
		NotificationID: res.NotificationID,
		AcceptedAt:     res.AcceptedAt.Format("2006-01-02T15:04:05.000Z07:00"),
	})
}

// outcomeForIngestError maps an ingest error to the metric label used on the
// writer submissions counter. Kept next to writeIngestError so the two stay
// in sync.
func (h *Handlers) outcomeForIngestError(err error) string {
	var verr *ingest.ValidationError
	if errors.As(err, &verr) {
		return metrics.OutcomeValidationFailed
	}
	var pe *pulsarlib.PublishError
	if errors.As(err, &pe) {
		if pe.Transient {
			return metrics.OutcomeSubstrateUnavailable
		}
	}
	return metrics.OutcomeInternal
}

// submitData is the JSON shape of `data` on a successful submission.
type submitData struct {
	NotificationID string `json:"notification_id"`
	AcceptedAt     string `json:"accepted_at"`
}

func decodeBody(r *http.Request, rid string) (*notificationpbv1.Notification, *problems.Problem) {
	ct := contentTypeOf(r)
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxRequestBodyBytes))
	if err != nil {
		return nil, problems.ValidationFailed(rid, r.URL.Path, "could not read request body", nil)
	}
	n := &notificationpbv1.Notification{}
	switch ct {
	case contentTypeJSON:
		if err := protojson.Unmarshal(body, n); err != nil {
			return nil, problems.ValidationFailed(rid, r.URL.Path, "invalid JSON: "+err.Error(), nil)
		}
	case contentTypeProtobuf, contentTypeProtobufAlt:
		if err := proto.Unmarshal(body, n); err != nil {
			return nil, problems.ValidationFailed(rid, r.URL.Path, "invalid protobuf: "+err.Error(), nil)
		}
	default:
		return nil, problems.UnsupportedContentType(rid, r.URL.Path, ct)
	}
	return n, nil
}

func contentTypeOf(r *http.Request) string {
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.TrimSpace(strings.ToLower(ct))
}

func (h *Handlers) writeIngestError(w http.ResponseWriter, r *http.Request, rid string, err error) {
	var verr *ingest.ValidationError
	if errors.As(err, &verr) {
		problems.Write(w, problems.ValidationFailed(rid, r.URL.Path, verr.Error(), verr.Fields))
		return
	}
	var pe *pulsarlib.PublishError
	if errors.As(err, &pe) {
		if pe.Transient {
			p := problems.SubstrateUnavailable(rid, r.URL.Path, "messaging substrate is temporarily unavailable")
			w.Header().Set("Retry-After", "5")
			problems.Write(w, p)
			return
		}
		problems.Write(w, problems.Internal(rid, r.URL.Path))
		return
	}
	problems.Write(w, problems.Internal(rid, r.URL.Path))
}
