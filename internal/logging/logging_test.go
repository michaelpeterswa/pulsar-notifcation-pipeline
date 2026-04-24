package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/logging"
)

func TestLogLevelToSlogLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"debug", slog.LevelDebug, false},
		{"INFO", slog.LevelInfo, false},
		{"Warn", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"nope", slog.LevelInfo, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := logging.LogLevelToSlogLevel(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if got != tc.want && !tc.wantErr {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewEmitsJSON(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logging.New(&buf, "info")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	logger.Info("hello", slog.String("key", "value"))

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("output not JSON: %v (%s)", err, buf.String())
	}
	if parsed["msg"] != "hello" {
		t.Errorf("msg = %v", parsed["msg"])
	}
	if parsed["key"] != "value" {
		t.Errorf("key = %v", parsed["key"])
	}
}

func TestNewRejectsBadLevel(t *testing.T) {
	if _, err := logging.New(&bytes.Buffer{}, "garbage"); err == nil {
		t.Fatal("expected error for bad level")
	}
}
