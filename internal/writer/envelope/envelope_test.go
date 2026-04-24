package envelope_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/envelope"
)

type submitData struct {
	NotificationID string `json:"notification_id"`
	AcceptedAt     string `json:"accepted_at"`
}

func TestNewPopulatesMeta(t *testing.T) {
	e := envelope.New("req-99", submitData{NotificationID: "n-1", AcceptedAt: "2026-04-23T18:00:00Z"})
	if e.Meta.RequestID != "req-99" {
		t.Errorf("Meta.RequestID = %q", e.Meta.RequestID)
	}
	if e.Data.NotificationID != "n-1" {
		t.Errorf("Data.NotificationID = %q", e.Data.NotificationID)
	}
}

func TestWriteEmitsJSONEnvelope(t *testing.T) {
	rr := httptest.NewRecorder()
	data := submitData{NotificationID: "n-1", AcceptedAt: "2026-04-23T18:00:00Z"}
	envelope.Write(rr, 202, "req-42", data)

	if got := rr.Result().Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q", got)
	}
	if rr.Result().StatusCode != 202 {
		t.Errorf("status = %d", rr.Result().StatusCode)
	}

	var decoded map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	dataOut, ok := decoded["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not an object: %v", decoded["data"])
	}
	if dataOut["notification_id"] != "n-1" {
		t.Errorf("data.notification_id = %v", dataOut["notification_id"])
	}
	metaOut, ok := decoded["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is not an object: %v", decoded["meta"])
	}
	if metaOut["request_id"] != "req-42" {
		t.Errorf("meta.request_id = %v", metaOut["request_id"])
	}
}

func TestRequestIDAlwaysPresent(t *testing.T) {
	// Even with an empty string, meta.request_id is emitted (never absent).
	rr := httptest.NewRecorder()
	envelope.Write(rr, 202, "", submitData{})
	var decoded map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&decoded)
	meta := decoded["meta"].(map[string]any)
	if _, ok := meta["request_id"]; !ok {
		t.Error("meta.request_id key missing")
	}
}
