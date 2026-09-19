package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	pstore_client "github.com/brotherlogic/pstore/client"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
)

func TestHealthz(t *testing.T) {
	s := NewServer(pstore_client.GetTestClient())
	req, _ := http.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK for /healthz, got %d", rr.Code)
	}
	if rr.Body.String() != "OK" {
		t.Errorf("expected 'OK' body, got %s", rr.Body.String())
	}
}

func TestWebhookIngress_NoSignature(t *testing.T) {
	s := NewServer(pstore_client.GetTestClient())
	req, _ := http.NewRequest("POST", "/webhook", bytes.NewBuffer([]byte("{}")))
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rr.Code)
	}
}

func TestWebhookIngress_ValidSignature(t *testing.T) {
	secret := "test-secret"
	os.Setenv("GH_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("GH_WEBHOOK_SECRET")

	s := NewServer(pstore_client.GetTestClient())
	payload := []byte(`{"action": "opened", "number": 123, "pull_request": {"title": "Test PR", "body": "Body text", "user": {"login": "user1"}}, "repository": {"full_name": "repo/test"}}`)

	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	signature := "sha256=" + hex.EncodeToString(h.Sum(nil))

	req, _ := http.NewRequest("POST", "/webhook", bytes.NewBuffer(payload))
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "pull_request")

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestWebhookIngress_InvalidJSON(t *testing.T) {
	secret := "test-secret"
	os.Setenv("GH_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("GH_WEBHOOK_SECRET")

	s := NewServer(pstore_client.GetTestClient())
	payload := []byte(`{invalid-json}`)

	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	signature := "sha256=" + hex.EncodeToString(h.Sum(nil))

	req, _ := http.NewRequest("POST", "/webhook", bytes.NewBuffer(payload))
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "pull_request")

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for invalid JSON, got %d", rr.Code)
	}
}

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
	s := newTestServer(pstore_client.GetTestClient())
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

func TestWebhookIngress_IssuesEvent(t *testing.T) {
	secret := "test-secret"
	os.Setenv("GH_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("GH_WEBHOOK_SECRET")

	handler := &mockWebhookHandler{}
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	sGrpc := grpc.NewServer()
	pb.RegisterWebhookHandlerServer(sGrpc, handler)
	go sGrpc.Serve(lis)
	defer sGrpc.Stop()

	s := newTestServer(pstore_client.GetTestClient())
	repo := "brotherlogic/ghwebhook"
	_, err = s.Register(context.Background(), &pb.RegistrationRequest{
		RepoFullName:   repo,
		ServiceAddress: lis.Addr().String(),
	})
	if err != nil {
		t.Fatalf("failed to register service: %v", err)
	}

	incomingLabels := map[string]string{
		"event_type": "issues",
		"status":     "200",
	}
	initialIncoming := getMetricValue("ghwebhook_incoming_events_total", incomingLabels)

	outgoingLabels := map[string]string{
		"event_type":  "issues",
		"destination": lis.Addr().String(),
		"status":      "success",
	}
	initialOutgoing := getMetricValue("ghwebhook_outgoing_events_total", outgoingLabels)

	payload := []byte(`{"action": "reopened", "number": 97, "issue": {"title": "PROBER TEST", "body": "Automated test", "user": {"login": "testuser"}}, "repository": {"full_name": "brotherlogic/ghwebhook"}}`)

	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	signature := "sha256=" + hex.EncodeToString(h.Sum(nil))

	req, _ := http.NewRequest("POST", "/webhook", bytes.NewBuffer(payload))
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "issues")

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	time.Sleep(100 * time.Millisecond)

	if len(handler.receivedEvents) != 1 {
		t.Fatalf("expected handler to receive 1 event, got %d", len(handler.receivedEvents))
	}

	ev := handler.receivedEvents[0]
	if ev.Header == nil || ev.Header.EventType != "issues" {
		t.Errorf("expected EventType 'issues', got %v", ev.Header)
	}
	issue := ev.GetIssue()
	if issue == nil {
		t.Fatalf("expected non-nil IssueEvent in payload")
	}
	if issue.Number != 97 || issue.Action != "reopened" || issue.Title != "PROBER TEST" {
		t.Errorf("unexpected issue details: %+v", issue)
	}
	if issue.User == nil || issue.User.Login != "testuser" {
		t.Errorf("unexpected issue user: %+v", issue.User)
	}
	if issue.Repository == nil || issue.Repository.FullName != repo {
		t.Errorf("unexpected issue repository: %+v", issue.Repository)
	}

	finalIncoming := getMetricValue("ghwebhook_incoming_events_total", incomingLabels)
	if finalIncoming != initialIncoming+1 {
		t.Errorf("expected incoming metric for 'issues' to increment by 1, got initial %f, final %f", initialIncoming, finalIncoming)
	}

	finalOutgoing := getMetricValue("ghwebhook_outgoing_events_total", outgoingLabels)
	if finalOutgoing != initialOutgoing+1 {
		t.Errorf("expected outgoing metric for 'issues' to increment by 1, got initial %f, final %f", initialOutgoing, finalOutgoing)
	}
}
