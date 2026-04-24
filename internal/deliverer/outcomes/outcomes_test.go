package outcomes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/outcomes"
)

func parseLast(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	last := lines[len(lines)-1]
	var out map[string]any
	if err := json.Unmarshal([]byte(last), &out); err != nil {
		t.Fatalf("unmarshal %q: %v", last, err)
	}
	return out
}

func TestRecordDelivered(t *testing.T) {
	buf := &bytes.Buffer{}
	rec := outcomes.NewSlogRecorder(outcomes.WithLogger(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	rec.Record(context.Background(), outcomes.Outcome{
		NotificationID: "nid-1",
		Status:         outcomes.StatusDelivered,
		Attempts:       1,
		FirstAttempt:   time.Unix(100, 0),
		LastAttempt:    time.Unix(100, 0),
	})
	o := parseLast(t, buf)
	if o["event"] != "notification.outcome" {
		t.Errorf("event = %v", o["event"])
	}
	if o["status"] != "delivered" {
		t.Errorf("status = %v", o["status"])
	}
	if o["level"] != "INFO" {
		t.Errorf("level = %v (want INFO for delivered)", o["level"])
	}
	if o["notification_id"] != "nid-1" {
		t.Errorf("notification_id = %v", o["notification_id"])
	}
}

func TestRecordFailedUsesWarn(t *testing.T) {
	buf := &bytes.Buffer{}
	rec := outcomes.NewSlogRecorder(outcomes.WithLogger(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	rec.Record(context.Background(), outcomes.Outcome{
		NotificationID:   "nid-2",
		Status:           outcomes.StatusPermanentlyFailed,
		Reason:           outcomes.ReasonEncryptionPolicyViolation,
		ProviderResponse: "",
		Attempts:         1,
	})
	o := parseLast(t, buf)
	if o["level"] != "WARN" {
		t.Errorf("level = %v (want WARN)", o["level"])
	}
	if o["reason"] != outcomes.ReasonEncryptionPolicyViolation {
		t.Errorf("reason = %v", o["reason"])
	}
}

func TestProviderResponseTruncated(t *testing.T) {
	buf := &bytes.Buffer{}
	rec := outcomes.NewSlogRecorder(outcomes.WithLogger(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	long := strings.Repeat("x", 600)
	rec.Record(context.Background(), outcomes.Outcome{
		NotificationID: "nid", Status: outcomes.StatusPermanentlyFailed, Reason: "x", Attempts: 1, ProviderResponse: long,
	})
	o := parseLast(t, buf)
	if got := o["provider_response"].(string); len(got) != 512 {
		t.Errorf("provider_response len = %d (want 512)", len(got))
	}
}
