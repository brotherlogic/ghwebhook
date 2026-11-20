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
	"reflect"
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

	payload := []byte(`{"action": "reopened", "issue": {"number": 97, "title": "PROBER TEST", "body": "Automated test", "user": {"login": "testuser"}}, "repository": {"full_name": "brotherlogic/ghwebhook"}}`)

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

func TestWebhookIngress_IssuesEvent_NestedNumberOnly(t *testing.T) {
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

	payload := []byte(`{"action": "reopened", "issue": {"number": 97, "title": "PROBER TEST", "body": "Automated test", "user": {"login": "testuser"}}, "repository": {"full_name": "brotherlogic/ghwebhook"}}`)

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
	issue := ev.GetIssue()
	if issue == nil {
		t.Fatalf("expected non-nil IssueEvent in payload")
	}
	if issue.Number != 97 {
		t.Errorf("expected issue.Number = 97, got %d", issue.Number)
	}
}

func TestWebhookIngress_IssuesEvent_FallbackTopLevelNumber(t *testing.T) {
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

	// Payload with number only at top level (legacy/fallback scenario)
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
	issue := ev.GetIssue()
	if issue == nil {
		t.Fatalf("expected non-nil IssueEvent in payload")
	}
	if issue.Number != 97 {
		t.Errorf("expected fallback issue.Number = 97, got %d", issue.Number)
	}
}

func TestWebhookIngress_PullRequestEvent_NestedNumber(t *testing.T) {
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

	// Payload with number only inside pull_request
	payload := []byte(`{"action": "opened", "pull_request": {"number": 42, "title": "PR Title", "body": "PR Body", "user": {"login": "pruser"}}, "repository": {"full_name": "brotherlogic/ghwebhook"}}`)

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

	time.Sleep(100 * time.Millisecond)

	if len(handler.receivedEvents) != 1 {
		t.Fatalf("expected handler to receive 1 event, got %d", len(handler.receivedEvents))
	}

	ev := handler.receivedEvents[0]
	pr := ev.GetPullRequest()
	if pr == nil {
		t.Fatalf("expected non-nil PullRequestEvent in payload")
	}
	if pr.Number != 42 {
		t.Errorf("expected pr.Number = 42, got %d", pr.Number)
	}
}

func TestWebhookIngress_ExtendedEvents(t *testing.T) {
	secret := "test-secret"
	os.Setenv("GH_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("GH_WEBHOOK_SECRET")

	repo := "brotherlogic/ghwebhook"

	tests := []struct {
		name              string
		eventHeader       string
		expectedMetricEvt string
		payload           string
		verify            func(t *testing.T, ev *pb.WebhookEvent)
	}{
		{
			name:              "enriched pull_request",
			eventHeader:       "pull_request",
			expectedMetricEvt: "pull_request",
			payload: `{
				"action": "closed",
				"number": 42,
				"pull_request": {
					"number": 42,
					"title": "Add login",
					"body": "Implements login",
					"state": "closed",
					"merged": true,
					"head": {"ref": "feat/login", "sha": "abc1234"},
					"base": {"ref": "main"},
					"user": {"login": "author1"}
				},
				"repository": {"full_name": "brotherlogic/ghwebhook"}
			}`,
			verify: func(t *testing.T, ev *pb.WebhookEvent) {
				t.Helper()
				pr := ev.GetPullRequest()
				if pr == nil {
					t.Fatalf("expected non-nil PullRequestEvent")
				}
				if pr.Action != "closed" || pr.Number != 42 || pr.Title != "Add login" || pr.Body != "Implements login" {
					t.Errorf("unexpected basic PR fields: %+v", pr)
				}
				if !pr.Merged || pr.HeadRef != "feat/login" || pr.HeadSha != "abc1234" || pr.BaseRef != "main" || pr.State != "closed" {
					t.Errorf("unexpected enriched PR fields: merged=%v head_ref=%q head_sha=%q base_ref=%q state=%q", pr.Merged, pr.HeadRef, pr.HeadSha, pr.BaseRef, pr.State)
				}
				if pr.User == nil || pr.User.Login != "author1" {
					t.Errorf("unexpected PR user: %+v", pr.User)
				}
				if pr.Repository == nil || pr.Repository.FullName != repo {
					t.Errorf("unexpected PR repository: %+v", pr.Repository)
				}
			},
		},
		{
			name:              "enriched issues",
			eventHeader:       "issues",
			expectedMetricEvt: "issues",
			payload: `{
				"action": "labeled",
				"issue": {
					"number": 97,
					"title": "Fix bug",
					"body": "Issue body",
					"state": "open",
					"labels": [{"name": "speculate-stalled"}, {"name": "bug"}],
					"user": {"login": "reporter"}
				},
				"repository": {"full_name": "brotherlogic/ghwebhook"}
			}`,
			verify: func(t *testing.T, ev *pb.WebhookEvent) {
				t.Helper()
				issue := ev.GetIssue()
				if issue == nil {
					t.Fatalf("expected non-nil IssueEvent")
				}
				if issue.Action != "labeled" || issue.Number != 97 || issue.Title != "Fix bug" || issue.Body != "Issue body" {
					t.Errorf("unexpected basic issue fields: %+v", issue)
				}
				if issue.State != "open" {
					t.Errorf("expected issue.State = 'open', got %q", issue.State)
				}
				wantLabels := []string{"speculate-stalled", "bug"}
				if !reflect.DeepEqual(issue.Labels, wantLabels) {
					t.Errorf("expected labels %v, got %v", wantLabels, issue.Labels)
				}
				if issue.User == nil || issue.User.Login != "reporter" {
					t.Errorf("unexpected issue user: %+v", issue.User)
				}
				if issue.Repository == nil || issue.Repository.FullName != repo {
					t.Errorf("unexpected issue repository: %+v", issue.Repository)
				}
			},
		},
		{
			name:              "push",
			eventHeader:       "push",
			expectedMetricEvt: "push",
			payload: `{
				"ref": "refs/heads/main",
				"before": "1111111",
				"after": "2222222",
				"sender": {"login": "pusher"},
				"repository": {"full_name": "brotherlogic/ghwebhook"}
			}`,
			verify: func(t *testing.T, ev *pb.WebhookEvent) {
				t.Helper()
				push := ev.GetPush()
				if push == nil {
					t.Fatalf("expected non-nil PushEvent")
				}
				if push.Ref != "refs/heads/main" || push.Before != "1111111" || push.After != "2222222" {
					t.Errorf("unexpected push fields: %+v", push)
				}
				if push.Sender == nil || push.Sender.Login != "pusher" {
					t.Errorf("unexpected push sender: %+v", push.Sender)
				}
				if push.Repository == nil || push.Repository.FullName != repo {
					t.Errorf("unexpected push repository: %+v", push.Repository)
				}
			},
		},
		{
			name:              "pull_request_review with nested pull_request number",
			eventHeader:       "pull_request_review",
			expectedMetricEvt: "pull_request_review",
			payload: `{
				"action": "submitted",
				"review": {
					"state": "approved",
					"commit_id": "deadbeef",
					"user": {"login": "owner"}
				},
				"pull_request": {
					"number": 55
				},
				"repository": {"full_name": "brotherlogic/ghwebhook"}
			}`,
			verify: func(t *testing.T, ev *pb.WebhookEvent) {
				t.Helper()
				review := ev.GetPullRequestReview()
				if review == nil {
					t.Fatalf("expected non-nil PullRequestReviewEvent")
				}
				if review.Action != "submitted" || review.State != "approved" || review.CommitId != "deadbeef" || review.PullRequestNumber != 55 {
					t.Errorf("unexpected pull_request_review fields: %+v", review)
				}
				if review.Reviewer == nil || review.Reviewer.Login != "owner" {
					t.Errorf("unexpected reviewer: %+v", review.Reviewer)
				}
				if review.Repository == nil || review.Repository.FullName != repo {
					t.Errorf("unexpected pull_request_review repository: %+v", review.Repository)
				}
			},
		},
		{
			name:              "pull_request_review with top-level fallback number",
			eventHeader:       "pull_request_review",
			expectedMetricEvt: "pull_request_review",
			payload: `{
				"action": "submitted",
				"number": 55,
				"review": {
					"state": "approved",
					"commit_id": "deadbeef",
					"user": {"login": "owner"}
				},
				"repository": {"full_name": "brotherlogic/ghwebhook"}
			}`,
			verify: func(t *testing.T, ev *pb.WebhookEvent) {
				t.Helper()
				review := ev.GetPullRequestReview()
				if review == nil {
					t.Fatalf("expected non-nil PullRequestReviewEvent")
				}
				if review.Action != "submitted" || review.State != "approved" || review.CommitId != "deadbeef" || review.PullRequestNumber != 55 {
					t.Errorf("unexpected pull_request_review fields: %+v", review)
				}
				if review.Reviewer == nil || review.Reviewer.Login != "owner" {
					t.Errorf("unexpected reviewer: %+v", review.Reviewer)
				}
				if review.Repository == nil || review.Repository.FullName != repo {
					t.Errorf("unexpected pull_request_review repository: %+v", review.Repository)
				}
			},
		},
		{
			name:              "check_suite",
			eventHeader:       "check_suite",
			expectedMetricEvt: "check_suite",
			payload: `{
				"action": "completed",
				"check_suite": {
					"status": "completed",
					"conclusion": "failure",
					"head_branch": "feat/login",
					"head_sha": "abc1234"
				},
				"repository": {"full_name": "brotherlogic/ghwebhook"}
			}`,
			verify: func(t *testing.T, ev *pb.WebhookEvent) {
				t.Helper()
				cs := ev.GetCheckSuite()
				if cs == nil {
					t.Fatalf("expected non-nil CheckSuiteEvent")
				}
				if cs.Action != "completed" || cs.Status != "completed" || cs.Conclusion != "failure" || cs.HeadBranch != "feat/login" || cs.HeadSha != "abc1234" {
					t.Errorf("unexpected check_suite fields: %+v", cs)
				}
				if cs.Repository == nil || cs.Repository.FullName != repo {
					t.Errorf("unexpected check_suite repository: %+v", cs.Repository)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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
			_, err = s.Register(context.Background(), &pb.RegistrationRequest{
				RepoFullName:   repo,
				ServiceAddress: lis.Addr().String(),
			})
			if err != nil {
				t.Fatalf("failed to register service: %v", err)
			}

			incomingLabels := map[string]string{
				"event_type": tc.expectedMetricEvt,
				"status":     "200",
			}
			initialIncoming := getMetricValue("ghwebhook_incoming_events_total", incomingLabels)

			outgoingLabels := map[string]string{
				"event_type":  tc.expectedMetricEvt,
				"destination": lis.Addr().String(),
				"status":      "success",
			}
			initialOutgoing := getMetricValue("ghwebhook_outgoing_events_total", outgoingLabels)

			payloadBytes := []byte(tc.payload)
			h := hmac.New(sha256.New, []byte(secret))
			h.Write(payloadBytes)
			signature := "sha256=" + hex.EncodeToString(h.Sum(nil))

			req, _ := http.NewRequest("POST", "/webhook", bytes.NewBuffer(payloadBytes))
			req.Header.Set("X-Hub-Signature-256", signature)
			req.Header.Set("X-GitHub-Event", tc.eventHeader)

			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
			}

			if len(handler.receivedEvents) != 1 {
				t.Fatalf("expected handler to receive 1 event, got %d", len(handler.receivedEvents))
			}

			ev := handler.receivedEvents[0]
			if ev.Header == nil || ev.Header.EventType != tc.eventHeader {
				t.Errorf("expected EventType %q, got %+v", tc.eventHeader, ev.Header)
			}
			tc.verify(t, ev)

			finalIncoming := getMetricValue("ghwebhook_incoming_events_total", incomingLabels)
			if finalIncoming != initialIncoming+1 {
				t.Errorf("expected incoming metric for %q to increment by 1, got initial %f, final %f", tc.expectedMetricEvt, initialIncoming, finalIncoming)
			}

			finalOutgoing := getMetricValue("ghwebhook_outgoing_events_total", outgoingLabels)
			if finalOutgoing != initialOutgoing+1 {
				t.Errorf("expected outgoing metric for %q to increment by 1, got initial %f, final %f", tc.expectedMetricEvt, initialOutgoing, finalOutgoing)
			}
		})
	}
}
