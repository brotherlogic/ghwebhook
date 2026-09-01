package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	pstore_client "github.com/brotherlogic/pstore/client"
	pstore_pb "github.com/brotherlogic/pstore/proto"
	"github.com/google/go-github/v69/github"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestRegister(t *testing.T) {
	// Initialize in-memory pstore client
	s := NewServer(pstore_client.GetTestClient())

	req := &pb.RegistrationRequest{
		RepoFullName:   "brotherlogic/ghwebhook",
		ServiceAddress: "localhost:50051",
	}

	resp, err := s.Register(context.Background(), req)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if !resp.Success {
		t.Errorf("Expected success, got failure: %s", resp.Message)
	}

	// Verify that the key exists in pstore
	key := fmt.Sprintf("ghwebhook/reg/%s/%s", req.RepoFullName, req.ServiceAddress)
	readResp, err := s.pstore.Read(context.Background(), &pstore_pb.ReadRequest{Key: key})
	if err != nil {
		t.Fatalf("Read from pstore failed: %v", err)
	}

	if readResp.Value == nil {
		t.Fatal("Expected value in pstore, got nil")
	}

	// Verify the content using manual unmarshal because TestClient drops type URL
	readReq := &pb.RegistrationRequest{}
	err = proto.Unmarshal(readResp.Value.Value, readReq)
	if err != nil {
		t.Fatalf("Failed to unmarshal pstore value: %v", err)
	}

	if readReq.RepoFullName != req.RepoFullName || readReq.ServiceAddress != req.ServiceAddress {
		t.Errorf("Stored data mismatch. Got %v, want %v", readReq, req)
	}
}

func TestGetRegistrations(t *testing.T) {
	s := NewServer(pstore_client.GetTestClient())

	repo := "brotherlogic/ghwebhook"
	services := []string{"localhost:50051", "localhost:50052"}

	for _, addr := range services {
		_, err := s.Register(context.Background(), &pb.RegistrationRequest{
			RepoFullName:   repo,
			ServiceAddress: addr,
		})
		if err != nil {
			t.Fatalf("Failed to register %s: %v", addr, err)
		}
	}

	regs, err := s.getRegistrations(context.Background(), repo)
	if err != nil {
		t.Fatalf("getRegistrations failed: %v", err)
	}

	if len(regs) != 2 {
		t.Errorf("Expected 2 registrations, got %d", len(regs))
	}
}

func TestMetricsRegistered(t *testing.T) {
	// Initialize label values to trigger registration in gatherer and verify label count
	IncomingEventsTotal.WithLabelValues("pull_request", "200")
	OutgoingEventsTotal.WithLabelValues("pull_request", "http://localhost:8080", "success")
	RegistrationsTotal.WithLabelValues("brotherlogic/ghwebhook", "http://localhost:8080")
	RegistrationStrikesTotal.WithLabelValues("brotherlogic/ghwebhook", "http://localhost:8080")
	RegistrationRemovalsTotal.WithLabelValues("brotherlogic/ghwebhook", "http://localhost:8080", "max_strikes")

	metricNames := []string{
		"ghwebhook_incoming_events_total",
		"ghwebhook_outgoing_events_total",
		"ghwebhook_registrations_total",
		"ghwebhook_registration_strikes_total",
		"ghwebhook_registration_removals_total",
	}

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	found := make(map[string]bool)
	for _, mf := range mfs {
		found[mf.GetName()] = true
	}

	for _, name := range metricNames {
		if !found[name] {
			var gathered []string
			for k := range found {
				gathered = append(gathered, k)
			}
			t.Errorf("metric %s not found in default registry. Gathered: %v", name, gathered)
		}
	}
}

func TestRegistrationMetrics(t *testing.T) {
	ps := pstore_client.GetTestClient()
	s := NewServer(ps)

	repo := "metrics-test/repo"
	addr := "127.0.0.1:9090"

	// Reset/initialize metrics to 0
	RegistrationsTotal.WithLabelValues(repo, addr).Set(0)
	RegistrationStrikesTotal.DeleteLabelValues(repo, addr)
	RegistrationRemovalsTotal.DeleteLabelValues(repo, addr, "max_strikes")

	// 1. Register a service and check that RegistrationsTotal is set to 1.0
	_, err := s.Register(context.Background(), &pb.RegistrationRequest{
		RepoFullName:   repo,
		ServiceAddress: addr,
	})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	val := testutil.ToFloat64(RegistrationsTotal.WithLabelValues(repo, addr))
	if val != 1.0 {
		t.Errorf("Expected RegistrationsTotal to be 1.0, got %f", val)
	}

	// 2. Clear local memory and test ScanRegistrations (simulating startup scan)
	RegistrationsTotal.WithLabelValues(repo, addr).Set(0)
	err = s.ScanRegistrations(context.Background())
	if err != nil {
		t.Fatalf("ScanRegistrations failed: %v", err)
	}
	val = testutil.ToFloat64(RegistrationsTotal.WithLabelValues(repo, addr))
	if val != 1.0 {
		t.Errorf("Expected ScanRegistrations to restore RegistrationsTotal to 1.0, got %f", val)
	}

	// 3. Test strikes and removal metrics
	// Set up always-failing handler
	handler := &failingWebhookHandler{maxFails: 100}
	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	grpcSrv := grpc.NewServer()
	pb.RegisterWebhookHandlerServer(grpcSrv, handler)
	go grpcSrv.Serve(lis)
	defer grpcSrv.Stop()

	testAddr := lis.Addr().String()
	s.backoffs = []time.Duration{1 * time.Millisecond}

	// Register the test handler address
	_, err = s.Register(context.Background(), &pb.RegistrationRequest{
		RepoFullName:   repo,
		ServiceAddress: testAddr,
	})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Initial strikes should be 0
	strikeVal := testutil.ToFloat64(RegistrationStrikesTotal.WithLabelValues(repo, testAddr))
	if strikeVal != 0 {
		t.Errorf("Expected initial strikes to be 0, got %f", strikeVal)
	}

	// Trigger 3 failed deliveries
	event := &pb.WebhookEvent{Header: &pb.EventHeader{EventType: "pull_request"}}
	for i := 0; i < 3; i++ {
		s.routeEvent(context.Background(), event, repo)
	}

	// Verify strikes accumulated (should be 3)
	strikeVal = testutil.ToFloat64(RegistrationStrikesTotal.WithLabelValues(repo, testAddr))
	if strikeVal != 3 {
		t.Errorf("Expected strikes to be 3, got %f", strikeVal)
	}

	// Verify removal count and registrations gauge reset to 0
	removalVal := testutil.ToFloat64(RegistrationRemovalsTotal.WithLabelValues(repo, testAddr, "max_strikes"))
	if removalVal != 1.0 {
		t.Errorf("Expected removals to be 1.0, got %f", removalVal)
	}

	regVal := testutil.ToFloat64(RegistrationsTotal.WithLabelValues(repo, testAddr))
	if regVal != 0 {
		t.Errorf("Expected RegistrationsTotal for removed client to be 0, got %f", regVal)
	}
}

func TestUnregister_Success(t *testing.T) {
	ps := pstore_client.GetTestClient()
	s := NewServer(ps)

	repo := "brotherlogic/ghwebhook"
	addr := "localhost:50051"

	// Register service
	_, err := s.Register(context.Background(), &pb.RegistrationRequest{
		RepoFullName:   repo,
		ServiceAddress: addr,
	})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Set in-memory strike to verify it gets cleaned up
	key := fmt.Sprintf("ghwebhook/reg/%s/%s", repo, addr)
	s.strikeLock.Lock()
	s.strikes[key] = 2
	s.strikeLock.Unlock()

	// Verify metric gauge is 1
	if val := testutil.ToFloat64(RegistrationsTotal.WithLabelValues(repo, addr)); val != 1.0 {
		t.Errorf("Expected RegistrationsTotal to be 1.0, got %f", val)
	}

	// Call Unregister
	resp, err := s.Unregister(context.Background(), &pb.UnregisterRequest{
		RepoFullName:   repo,
		ServiceAddress: addr,
	})
	if err != nil {
		t.Fatalf("Unregister failed: %v", err)
	}
	if !resp.Success {
		t.Errorf("Expected success to be true, got false: %s", resp.Message)
	}

	// Verify pstore key is deleted
	readResp, err := s.pstore.Read(context.Background(), &pstore_pb.ReadRequest{Key: key})
	if err == nil && readResp.Value != nil && len(readResp.Value.Value) > 0 {
		t.Errorf("Expected key to be deleted from pstore, but found value: %v", readResp.Value)
	}

	// Verify in-memory strikes cleaned up
	s.strikeLock.Lock()
	if strikes, ok := s.strikes[key]; ok {
		t.Errorf("Expected strikes to be deleted for %s, but found %d", key, strikes)
	}
	s.strikeLock.Unlock()

	// Verify metric gauge is reset to 0
	if val := testutil.ToFloat64(RegistrationsTotal.WithLabelValues(repo, addr)); val != 0 {
		t.Errorf("Expected RegistrationsTotal to be 0 after Unregister, got %f", val)
	}
}

func TestUnregister_NotFound(t *testing.T) {
	ps := pstore_client.GetTestClient()
	s := NewServer(ps)

	_, err := s.Unregister(context.Background(), &pb.UnregisterRequest{
		RepoFullName:   "brotherlogic/ghwebhook",
		ServiceAddress: "nonexistent:50051",
	})
	if err == nil {
		t.Fatal("Expected error when unregistering non-existent registration, got nil")
	}

	if st, ok := status.FromError(err); !ok || st.Code() != codes.NotFound {
		t.Errorf("Expected codes.NotFound, got: %v", err)
	}
}

func TestUnregister_InvalidArgument(t *testing.T) {
	ps := pstore_client.GetTestClient()
	s := NewServer(ps)

	tests := []struct {
		name string
		req  *pb.UnregisterRequest
	}{
		{name: "nil request", req: nil},
		{name: "missing repo", req: &pb.UnregisterRequest{ServiceAddress: "localhost:50051"}},
		{name: "missing service_address", req: &pb.UnregisterRequest{RepoFullName: "brotherlogic/ghwebhook"}},
		{name: "empty both", req: &pb.UnregisterRequest{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Unregister(context.Background(), tt.req)
			if err == nil {
				t.Fatal("Expected error for invalid argument, got nil")
			}
			if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
				t.Errorf("Expected codes.InvalidArgument, got: %v", err)
			}
		})
	}
}

func TestUnregister_MultiServiceRetention(t *testing.T) {
	ps := pstore_client.GetTestClient()
	s := NewServer(ps)

	repo := "brotherlogic/ghwebhook"
	addr1 := "localhost:50051"
	addr2 := "localhost:50052"

	_, err := s.Register(context.Background(), &pb.RegistrationRequest{RepoFullName: repo, ServiceAddress: addr1})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	_, err = s.Register(context.Background(), &pb.RegistrationRequest{RepoFullName: repo, ServiceAddress: addr2})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Unregister first service
	_, err = s.Unregister(context.Background(), &pb.UnregisterRequest{RepoFullName: repo, ServiceAddress: addr1})
	if err != nil {
		t.Fatalf("Unregister failed: %v", err)
	}

	// Check remaining registrations
	regs, err := s.getRegistrations(context.Background(), repo)
	if err != nil {
		t.Fatalf("getRegistrations failed: %v", err)
	}
	if len(regs) != 1 {
		t.Fatalf("Expected 1 remaining registration, got %d", len(regs))
	}
	if regs[0].ServiceAddress != addr2 {
		t.Errorf("Expected remaining service to be %s, got %s", addr2, regs[0].ServiceAddress)
	}
}

type mockGitHubHookClient struct {
	listHooksFunc  func(ctx context.Context, owner, repo string) ([]*github.Hook, error)
	deleteHookFunc func(ctx context.Context, owner, repo string, hookID int64) error
}

func (m *mockGitHubHookClient) ListHooks(ctx context.Context, owner, repo string) ([]*github.Hook, error) {
	if m.listHooksFunc != nil {
		return m.listHooksFunc(ctx, owner, repo)
	}
	return nil, nil
}

func (m *mockGitHubHookClient) DeleteHook(ctx context.Context, owner, repo string, hookID int64) error {
	if m.deleteHookFunc != nil {
		return m.deleteHookFunc(ctx, owner, repo, hookID)
	}
	return nil
}

func TestDeleteGitHubWebhook_Success(t *testing.T) {
	ps := pstore_client.GetTestClient()
	ingressURL := "https://example.com/webhook"
	hookID := int64(98765)

	var deleteCalled atomic.Bool
	mockClient := &mockGitHubHookClient{
		listHooksFunc: func(ctx context.Context, owner, repo string) ([]*github.Hook, error) {
			if owner != "brotherlogic" || repo != "ghwebhook" {
				t.Errorf("Unexpected repo: %s/%s", owner, repo)
			}
			url := ingressURL
			otherURL := "https://other.com/webhook"
			otherID := int64(11111)
			return []*github.Hook{
				{
					ID: &otherID,
					Config: &github.HookConfig{
						URL: &otherURL,
					},
				},
				{
					ID: &hookID,
					Config: &github.HookConfig{
						URL: &url,
					},
				},
			}, nil
		},
		deleteHookFunc: func(ctx context.Context, owner, repo string, id int64) error {
			if id != hookID {
				t.Errorf("Unexpected hookID deleted: %d, want %d", id, hookID)
			}
			deleteCalled.Store(true)
			return nil
		},
	}

	s := NewServer(
		ps,
		WithGitHubClient(mockClient),
		WithIngressURL(ingressURL),
	)

	err := s.deleteGitHubWebhook(context.Background(), "brotherlogic/ghwebhook")
	if err != nil {
		t.Fatalf("deleteGitHubWebhook failed: %v", err)
	}

	if !deleteCalled.Load() {
		t.Errorf("Expected DeleteHook to be called, but it was not")
	}
}

func TestDeleteGitHubWebhook_404HandledAsSuccess(t *testing.T) {
	ps := pstore_client.GetTestClient()
	ingressURL := "https://example.com/webhook"
	hookID := int64(98765)

	mockClient := &mockGitHubHookClient{
		listHooksFunc: func(ctx context.Context, owner, repo string) ([]*github.Hook, error) {
			url := ingressURL
			return []*github.Hook{
				{
					ID: &hookID,
					Config: &github.HookConfig{
						URL: &url,
					},
				},
			}, nil
		},
		deleteHookFunc: func(ctx context.Context, owner, repo string, id int64) error {
			return &github.ErrorResponse{
				Response: &http.Response{StatusCode: http.StatusNotFound},
				Message:  "Not Found",
			}
		},
	}

	s := NewServer(
		ps,
		WithGitHubClient(mockClient),
		WithIngressURL(ingressURL),
	)

	err := s.deleteGitHubWebhook(context.Background(), "brotherlogic/ghwebhook")
	if err != nil {
		t.Errorf("Expected 404 to be treated as success, got error: %v", err)
	}
}

func TestDeleteGitHubWebhook_ConcurrencyGuard(t *testing.T) {
	ps := pstore_client.GetTestClient()
	repo := "brotherlogic/ghwebhook"

	var listCalled atomic.Bool
	mockClient := &mockGitHubHookClient{
		listHooksFunc: func(ctx context.Context, owner, repo string) ([]*github.Hook, error) {
			listCalled.Store(true)
			return nil, nil
		},
	}

	s := NewServer(
		ps,
		WithGitHubClient(mockClient),
		WithIngressURL("https://example.com/webhook"),
	)

	// Register a service before deletion check
	_, err := s.Register(context.Background(), &pb.RegistrationRequest{
		RepoFullName:   repo,
		ServiceAddress: "localhost:50051",
	})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Calling deleteGitHubWebhook should abort because registration exists
	err = s.deleteGitHubWebhook(context.Background(), repo)
	if err != nil {
		t.Fatalf("deleteGitHubWebhook returned unexpected error: %v", err)
	}

	if listCalled.Load() {
		t.Errorf("Expected deleteGitHubWebhook to abort due to concurrency guard, but ListHooks was called")
	}
}

func TestDeleteGitHubWebhook_MissingConfigOrMalformedRepo(t *testing.T) {
	ps := pstore_client.GetTestClient()

	mockClient := &mockGitHubHookClient{}

	tests := []struct {
		name       string
		server     *Server
		repo       string
	}{
		{
			name:   "nil ghClient",
			server: NewServer(ps, WithIngressURL("https://example.com/webhook")),
			repo:   "brotherlogic/ghwebhook",
		},
		{
			name:   "empty ingressURL",
			server: NewServer(ps, WithGitHubClient(mockClient), WithIngressURL("")),
			repo:   "brotherlogic/ghwebhook",
		},
		{
			name:   "malformed repo without slash",
			server: NewServer(ps, WithGitHubClient(mockClient), WithIngressURL("https://example.com/webhook")),
			repo:   "invalidrepo",
		},
		{
			name:   "empty repo string",
			server: NewServer(ps, WithGitHubClient(mockClient), WithIngressURL("https://example.com/webhook")),
			repo:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.server.deleteGitHubWebhook(context.Background(), tt.repo)
			if err != nil {
				t.Errorf("Expected nil error for graceful handling, got %v", err)
			}
		})
	}
}

func TestDeleteGitHubWebhook_TransientErrorRetry(t *testing.T) {
	ps := pstore_client.GetTestClient()
	ingressURL := "https://example.com/webhook"
	hookID := int64(12345)

	var listAttempts atomic.Int32
	var deleteAttempts atomic.Int32

	mockClient := &mockGitHubHookClient{
		listHooksFunc: func(ctx context.Context, owner, repo string) ([]*github.Hook, error) {
			attempts := listAttempts.Add(1)
			if attempts < 2 {
				return nil, errors.New("transient network failure")
			}
			url := ingressURL
			return []*github.Hook{
				{
					ID: &hookID,
					Config: &github.HookConfig{
						URL: &url,
					},
				},
			}, nil
		},
		deleteHookFunc: func(ctx context.Context, owner, repo string, id int64) error {
			attempts := deleteAttempts.Add(1)
			if attempts < 2 {
				return errors.New("transient delete failure")
			}
			return nil
		},
	}

	s := NewServer(
		ps,
		WithGitHubClient(mockClient),
		WithIngressURL(ingressURL),
	)
	s.backoffs = []time.Duration{1 * time.Millisecond, 2 * time.Millisecond}

	err := s.deleteGitHubWebhook(context.Background(), "brotherlogic/ghwebhook")
	if err != nil {
		t.Fatalf("deleteGitHubWebhook failed despite retries: %v", err)
	}

	if listAttempts.Load() != 2 {
		t.Errorf("Expected 2 list attempts, got %d", listAttempts.Load())
	}
	if deleteAttempts.Load() != 2 {
		t.Errorf("Expected 2 delete attempts, got %d", deleteAttempts.Load())
	}
}

func TestUnregister_TriggersAsyncWebhookDeletion(t *testing.T) {
	ps := pstore_client.GetTestClient()
	ingressURL := "https://example.com/webhook"
	hookID := int64(55555)

	deleted := make(chan bool, 1)
	mockClient := &mockGitHubHookClient{
		listHooksFunc: func(ctx context.Context, owner, repo string) ([]*github.Hook, error) {
			url := ingressURL
			return []*github.Hook{
				{
					ID: &hookID,
					Config: &github.HookConfig{
						URL: &url,
					},
				},
			}, nil
		},
		deleteHookFunc: func(ctx context.Context, owner, repo string, id int64) error {
			if id == hookID {
				deleted <- true
			}
			return nil
		},
	}

	s := NewServer(
		ps,
		WithGitHubClient(mockClient),
		WithIngressURL(ingressURL),
	)
	s.backoffs = []time.Duration{1 * time.Millisecond}

	repo := "brotherlogic/ghwebhook"
	addr := "localhost:50051"

	_, err := s.Register(context.Background(), &pb.RegistrationRequest{RepoFullName: repo, ServiceAddress: addr})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	_, err = s.Unregister(context.Background(), &pb.UnregisterRequest{RepoFullName: repo, ServiceAddress: addr})
	if err != nil {
		t.Fatalf("Unregister failed: %v", err)
	}

	select {
	case <-deleted:
		// Webhook deletion was triggered asynchronously and completed
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for asynchronous webhook deletion")
	}
}


