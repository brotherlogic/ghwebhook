package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	pstore_client "github.com/brotherlogic/pstore/client"
	"github.com/prometheus/client_golang/prometheus"
)

func getMetricValue(name string, labels map[string]string) float64 {
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return 0
	}
	for _, mf := range mfs {
		if mf.GetName() == name {
			for _, m := range mf.GetMetric() {
				match := true
				for _, l := range m.GetLabel() {
					val, ok := labels[l.GetName()]
					if !ok || val != l.GetValue() {
						match = false
						break
					}
				}
				if match && len(m.GetLabel()) == len(labels) {
					if m.Counter != nil {
						return m.Counter.GetValue()
					}
					if m.Gauge != nil {
						return m.Gauge.GetValue()
					}
				}
			}
		}
	}
	return 0
}

func TestWebhookIngressMetrics_SignatureFailure(t *testing.T) {
	s := NewServer(pstore_client.GetTestClient())
	
	labels := map[string]string{
		"event_type": "pull_request",
		"status":     "401",
	}
	initialVal := getMetricValue("ghwebhook_incoming_events_total", labels)

	req, _ := http.NewRequest("POST", "/webhook", bytes.NewBuffer([]byte("{}")))
	req.Header.Set("X-GitHub-Event", "pull_request")
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rr.Code)
	}

	finalVal := getMetricValue("ghwebhook_incoming_events_total", labels)
	if finalVal != initialVal+1 {
		t.Errorf("expected metric value to increase by 1, got initial %f, final %f", initialVal, finalVal)
	}
}

func TestWebhookIngressMetrics_UnknownEventType(t *testing.T) {
	secret := "test-secret"
	os.Setenv("GH_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("GH_WEBHOOK_SECRET")

	s := NewServer(pstore_client.GetTestClient())
	payload := []byte(`{"action": "opened", "repository": {"full_name": "repo/test"}}`)
	
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	signature := "sha256=" + hex.EncodeToString(h.Sum(nil))

	labels := map[string]string{
		"event_type": "unknown",
		"status":     "200",
	}
	initialVal := getMetricValue("ghwebhook_incoming_events_total", labels)

	req, _ := http.NewRequest("POST", "/webhook", bytes.NewBuffer(payload))
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "non_existent_event")
	
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}

	finalVal := getMetricValue("ghwebhook_incoming_events_total", labels)
	if finalVal != initialVal+1 {
		t.Errorf("expected metric value to increase by 1, got initial %f, final %f", initialVal, finalVal)
	}
}

func TestWebhookIngressMetrics_OutgoingDelivery(t *testing.T) {
	// Register a fake handler
	s := NewServer(pstore_client.GetTestClient())
	s.backoffs = []time.Duration{10 * time.Millisecond} // fast retry

	ctx := context.Background()
	
	// Register a destination address that will fail (no gRPC server listening)
	address := "127.0.0.1:54321"
	_, err := s.Register(ctx, &pb.RegistrationRequest{
		RepoFullName:   "repo/test",
		ServiceAddress: address,
	})
	if err != nil {
		t.Fatalf("failed to register: %v", err)
	}

	labels := map[string]string{
		"event_type":  "pull_request",
		"destination": address,
		"status":      "failure",
	}
	initialVal := getMetricValue("ghwebhook_outgoing_events_total", labels)

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "pull_request",
		},
		Payload: &pb.WebhookEvent_PullRequest{
			PullRequest: &pb.PullRequestEvent{},
		},
	}

	s.routeEvent(ctx, event, "repo/test")

	finalVal := getMetricValue("ghwebhook_outgoing_events_total", labels)
	if finalVal != initialVal+1 {
		t.Errorf("expected outgoing metric value to increase by 1 on failure, got initial %f, final %f", initialVal, finalVal)
	}
}
