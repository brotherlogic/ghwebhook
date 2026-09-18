package prober

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"github.com/google/go-github/v69/github"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestReceiveWebhook_NilRequest(t *testing.T) {
	p := NewProber()
	resp, err := p.ReceiveWebhook(context.Background(), nil)
	if err != nil {
		t.Fatalf("ReceiveWebhook with nil req returned error: %v", err)
	}
	if resp == nil || resp.Success {
		t.Errorf("ReceiveWebhook with nil req resp = %v, want success=false", resp)
	}
}

func TestReceiveWebhook_NonIssuePayload(t *testing.T) {
	p := NewProber(WithRepo("brotherlogic/ghwebhook"), WithTargetTitle("PROBER TEST"))

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "pull_request",
		},
		Payload: &pb.WebhookEvent_PullRequest{
			PullRequest: &pb.PullRequestEvent{
				Action: "opened",
				Number: 1,
				Title:  "PROBER TEST",
				Repository: &pb.Repository{
					FullName: "brotherlogic/ghwebhook",
				},
			},
		},
	}

	resp, err := p.ReceiveWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected success=true (handled/ignored), got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		t.Fatalf("expected no event on eventCh, got %v", ev)
	default:
		// OK
	}
}

func TestReceiveWebhook_RepoMismatch(t *testing.T) {
	p := NewProber(WithRepo("brotherlogic/ghwebhook"), WithTargetTitle("PROBER TEST"))

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "issues",
		},
		Payload: &pb.WebhookEvent_Issue{
			Issue: &pb.IssueEvent{
				Action: "opened",
				Number: 10,
				Title:  "PROBER TEST",
				Repository: &pb.Repository{
					FullName: "other/repo",
				},
			},
		},
	}

	resp, err := p.ReceiveWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected success=true, got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		t.Fatalf("expected no event dispatched on repo mismatch, got %v", ev)
	default:
		// OK
	}
}

func TestReceiveWebhook_TitleMismatch(t *testing.T) {
	p := NewProber(WithRepo("brotherlogic/ghwebhook"), WithTargetTitle("PROBER TEST"))

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "issues",
		},
		Payload: &pb.WebhookEvent_Issue{
			Issue: &pb.IssueEvent{
				Action: "opened",
				Number: 10,
				Title:  "SOME OTHER TITLE",
				Repository: &pb.Repository{
					FullName: "brotherlogic/ghwebhook",
				},
			},
		},
	}

	resp, err := p.ReceiveWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected success=true, got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		t.Fatalf("expected no event dispatched on title mismatch, got %v", ev)
	default:
		// OK
	}
}

func TestReceiveWebhook_IssueNumberMismatch(t *testing.T) {
	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithTargetIssueNumber(42),
	)

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "issues",
		},
		Payload: &pb.WebhookEvent_Issue{
			Issue: &pb.IssueEvent{
				Action: "opened",
				Number: 99,
				Title:  "PROBER TEST",
				Repository: &pb.Repository{
					FullName: "brotherlogic/ghwebhook",
				},
			},
		},
	}

	resp, err := p.ReceiveWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected success=true, got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		t.Fatalf("expected no event dispatched on number mismatch, got %v", ev)
	default:
		// OK
	}
}

func TestReceiveWebhook_ActionMismatch(t *testing.T) {
	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithTargetAction("closed"),
	)

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "issues",
		},
		Payload: &pb.WebhookEvent_Issue{
			Issue: &pb.IssueEvent{
				Action: "opened",
				Number: 10,
				Title:  "PROBER TEST",
				Repository: &pb.Repository{
					FullName: "brotherlogic/ghwebhook",
				},
			},
		},
	}

	resp, err := p.ReceiveWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected success=true, got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		t.Fatalf("expected no event dispatched on action mismatch, got %v", ev)
	default:
		// OK
	}
}

func TestReceiveWebhook_MatchingEvent_Dispatched(t *testing.T) {
	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithTargetIssueNumber(42),
		WithTargetAction("reopened"),
	)

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "issues",
		},
		Payload: &pb.WebhookEvent_Issue{
			Issue: &pb.IssueEvent{
				Action: "reopened",
				Number: 42,
				Title:  "PROBER TEST",
				Repository: &pb.Repository{
					FullName: "brotherlogic/ghwebhook",
				},
			},
		},
	}

	resp, err := p.ReceiveWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected success=true, got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		if ev.GetIssue().GetNumber() != 42 {
			t.Errorf("ev.Number = %d, want 42", ev.GetIssue().GetNumber())
		}
		if ev.GetIssue().GetAction() != "reopened" {
			t.Errorf("ev.Action = %q, want 'reopened'", ev.GetIssue().GetAction())
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for event on eventCh")
	}
}

func TestStartGRPCServer_LifecycleAndRPC(t *testing.T) {
	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.StartGRPCServer(ctx); err != nil {
		t.Fatalf("StartGRPCServer failed: %v", err)
	}
	defer p.StopGRPCServer()

	// Wait for listener to be active
	addr := p.ListenAddr()
	if addr == "" || addr == "127.0.0.1:0" {
		t.Fatalf("ListenAddr was not updated with dynamic port: %s", addr)
	}

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial prober gRPC server: %v", err)
	}
	defer conn.Close()

	client := pb.NewWebhookHandlerClient(conn)

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "issues",
		},
		Payload: &pb.WebhookEvent_Issue{
			Issue: &pb.IssueEvent{
				Action: "opened",
				Number: 100,
				Title:  "PROBER TEST",
				Repository: &pb.Repository{
					FullName: "brotherlogic/ghwebhook",
				},
			},
		},
	}

	resp, err := client.ReceiveWebhook(ctx, event)
	if err != nil {
		t.Fatalf("client.ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected resp.Success = true, got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		if ev.GetIssue().GetNumber() != 100 {
			t.Errorf("ev.Number = %d, want 100", ev.GetIssue().GetNumber())
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for event on eventCh from gRPC call")
	}

	// Test graceful shutdown
	p.StopGRPCServer()

	// Verify server stopped
	time.Sleep(100 * time.Millisecond)
	_, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if dialErr == nil {
		t.Error("expected connection failure after StopGRPCServer, but connection succeeded")
	}
}

func TestStartGRPCServer_ContextCancellation(t *testing.T) {
	p := NewProber(
		WithListenAddr("127.0.0.1:0"),
	)

	ctx, cancel := context.WithCancel(context.Background())

	if err := p.StartGRPCServer(ctx); err != nil {
		t.Fatalf("StartGRPCServer failed: %v", err)
	}

	addr := p.ListenAddr()
	cancel()

	// Give time for goroutine watching ctx.Done() to execute GracefulStop
	time.Sleep(200 * time.Millisecond)

	_, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if dialErr == nil {
		t.Error("expected connection failure after context cancel, but connection succeeded")
	}
}

func TestStartGRPCServer_InvalidAddress(t *testing.T) {
	p := NewProber(
		WithListenAddr("invalid-hostname-12345.local:99999"),
	)

	err := p.StartGRPCServer(context.Background())
	if err == nil {
		p.StopGRPCServer()
		t.Fatal("expected error starting gRPC server on invalid address, got nil")
	}
}

type mockRegistrationServiceClient struct {
	registerFunc   func(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error)
	unregisterFunc func(ctx context.Context, in *pb.UnregisterRequest, opts ...grpc.CallOption) (*pb.UnregisterResponse, error)
}

func (m *mockRegistrationServiceClient) Register(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error) {
	if m.registerFunc != nil {
		return m.registerFunc(ctx, in, opts...)
	}
	return &pb.RegistrationResponse{Success: true}, nil
}

func (m *mockRegistrationServiceClient) Unregister(ctx context.Context, in *pb.UnregisterRequest, opts ...grpc.CallOption) (*pb.UnregisterResponse, error) {
	if m.unregisterFunc != nil {
		return m.unregisterFunc(ctx, in, opts...)
	}
	return &pb.UnregisterResponse{Success: true}, nil
}

func mockActiveHookClient() *MockGitHubHookClient {
	hookID := int64(1)
	active := true
	return &MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			return []*github.Hook{
				{
					ID:     &hookID,
					Active: &active,
					Events: []string{"issues"},
				},
			}, nil
		},
	}
}

func TestProber_Success_CreatedIssue(t *testing.T) {
	var registeredRepo, registeredAddr string
	var unregisteredRepo, unregisteredAddr string
	var createdTitle string
	var closedIssueNumber int

	regClient := &mockRegistrationServiceClient{
		registerFunc: func(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error) {
			registeredRepo = in.GetRepoFullName()
			registeredAddr = in.GetServiceAddress()
			return &pb.RegistrationResponse{Success: true}, nil
		},
		unregisterFunc: func(ctx context.Context, in *pb.UnregisterRequest, opts ...grpc.CallOption) (*pb.UnregisterResponse, error) {
			unregisteredRepo = in.GetRepoFullName()
			unregisteredAddr = in.GetServiceAddress()
			return &pb.UnregisterResponse{Success: true}, nil
		},
	}

	issueNumber := 101
	ghClient := &MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{}, nil // No existing issue
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			createdTitle = req.GetTitle()
			num := issueNumber
			state := "open"
			title := req.GetTitle()
			return &github.Issue{
				Number: &num,
				State:  &state,
				Title:  &title,
			}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			if req.GetState() == "closed" {
				closedIssueNumber = number
			}
			state := req.GetState()
			return &github.Issue{Number: &number, State: &state}, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:0"),
		WithTimeout(2*time.Second),
		WithRegistrationClient(regClient),
		WithGitHubClient(ghClient),
		WithHookClient(mockActiveHookClient()),
	)

	// In a goroutine, simulate ghwebhook delivering the matching webhook event
	go func() {
		time.Sleep(100 * time.Millisecond)
		p.eventCh <- &pb.WebhookEvent{
			Header: &pb.EventHeader{EventType: "issues"},
			Payload: &pb.WebhookEvent_Issue{
				Issue: &pb.IssueEvent{
					Action: "opened",
					Number: int32(issueNumber),
					Title:  "PROBER TEST",
					Repository: &pb.Repository{
						FullName: "brotherlogic/ghwebhook",
					},
				},
			},
		}
	}()

	res, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Prober.Run returned unexpected error: %v", err)
	}
	if res.Status != StatusSuccess {
		t.Fatalf("expected StatusSuccess (0), got %v", res.Status)
	}
	if res.IssueNumber != issueNumber {
		t.Errorf("res.IssueNumber = %d, want %d", res.IssueNumber, issueNumber)
	}
	if res.Action != "opened" {
		t.Errorf("res.Action = %q, want 'opened'", res.Action)
	}
	if registeredAddr == "" {
		t.Error("expected registeredAddr to be non-empty")
	}
	if unregisteredAddr == "" {
		t.Error("expected unregisteredAddr to be non-empty")
	}
	if registeredRepo != "brotherlogic/ghwebhook" {
		t.Errorf("registeredRepo = %q, want 'brotherlogic/ghwebhook'", registeredRepo)
	}
	if unregisteredRepo != "brotherlogic/ghwebhook" {
		t.Errorf("unregisteredRepo = %q, want 'brotherlogic/ghwebhook'", unregisteredRepo)
	}
	if createdTitle != "PROBER TEST" {
		t.Errorf("createdTitle = %q, want 'PROBER TEST'", createdTitle)
	}
	if closedIssueNumber != issueNumber {
		t.Errorf("closedIssueNumber = %d, want %d", closedIssueNumber, issueNumber)
	}
}

func TestProber_Success_ReopenedIssue(t *testing.T) {
	issueNumber := 202
	var reopenedIssueNumber int
	var closedIssueNumber int

	regClient := &mockRegistrationServiceClient{}
	ghClient := &MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			num := issueNumber
			state := "closed"
			title := "PROBER TEST"
			return []*github.Issue{{
				Number: &num,
				State:  &state,
				Title:  &title,
			}}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			if req.GetState() == "open" {
				reopenedIssueNumber = number
			}
			if req.GetState() == "closed" {
				closedIssueNumber = number
			}
			state := req.GetState()
			return &github.Issue{Number: &number, State: &state}, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:0"),
		WithTimeout(2*time.Second),
		WithRegistrationClient(regClient),
		WithGitHubClient(ghClient),
		WithHookClient(mockActiveHookClient()),
	)

	go func() {
		time.Sleep(100 * time.Millisecond)
		p.eventCh <- &pb.WebhookEvent{
			Header: &pb.EventHeader{EventType: "issues"},
			Payload: &pb.WebhookEvent_Issue{
				Issue: &pb.IssueEvent{
					Action: "reopened",
					Number: int32(issueNumber),
					Title:  "PROBER TEST",
					Repository: &pb.Repository{
						FullName: "brotherlogic/ghwebhook",
					},
				},
			},
		}
	}()

	res, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Prober.Run error: %v", err)
	}
	if res.Status != StatusSuccess {
		t.Fatalf("expected StatusSuccess (0), got %v", res.Status)
	}
	if res.Action != "reopened" {
		t.Errorf("res.Action = %q, want 'reopened'", res.Action)
	}
	if reopenedIssueNumber != issueNumber {
		t.Errorf("reopenedIssueNumber = %d, want %d", reopenedIssueNumber, issueNumber)
	}
	if closedIssueNumber != issueNumber {
		t.Errorf("closedIssueNumber = %d, want %d (after test cleanup)", closedIssueNumber, issueNumber)
	}
}

func TestProber_Success_ClosedIssue(t *testing.T) {
	issueNumber := 303
	var closedIssueNumber int

	regClient := &mockRegistrationServiceClient{}
	ghClient := &MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			num := issueNumber
			state := "open"
			title := "PROBER TEST"
			return []*github.Issue{{
				Number: &num,
				State:  &state,
				Title:  &title,
			}}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			if req.GetState() == "closed" {
				closedIssueNumber = number
			}
			state := req.GetState()
			return &github.Issue{Number: &number, State: &state}, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:0"),
		WithTimeout(2*time.Second),
		WithRegistrationClient(regClient),
		WithGitHubClient(ghClient),
		WithHookClient(mockActiveHookClient()),
	)

	go func() {
		time.Sleep(100 * time.Millisecond)
		p.eventCh <- &pb.WebhookEvent{
			Header: &pb.EventHeader{EventType: "issues"},
			Payload: &pb.WebhookEvent_Issue{
				Issue: &pb.IssueEvent{
					Action: "closed",
					Number: int32(issueNumber),
					Title:  "PROBER TEST",
					Repository: &pb.Repository{
						FullName: "brotherlogic/ghwebhook",
					},
				},
			},
		}
	}()

	res, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Prober.Run error: %v", err)
	}
	if res.Status != StatusSuccess {
		t.Fatalf("expected StatusSuccess (0), got %v", res.Status)
	}
	if res.Action != "closed" {
		t.Errorf("res.Action = %q, want 'closed'", res.Action)
	}
	if closedIssueNumber != issueNumber {
		t.Errorf("closedIssueNumber = %d, want %d", closedIssueNumber, issueNumber)
	}
}

func TestProber_HardFailure_Timeout(t *testing.T) {
	issueNumber := 404

	regClient := &mockRegistrationServiceClient{}
	ghClient := &MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			num := issueNumber
			state := "open"
			title := "PROBER TEST"
			return []*github.Issue{{
				Number: &num,
				State:  &state,
				Title:  &title,
			}}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			return &github.Issue{}, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:0"),
		WithTimeout(100*time.Millisecond), // Short timeout
		WithRegistrationClient(regClient),
		WithGitHubClient(ghClient),
		WithHookClient(mockActiveHookClient()),
	)

	res, err := p.Run(context.Background())
	if res.Status != StatusHardFailure {
		t.Fatalf("expected StatusHardFailure (1), got %v (err: %v)", res.Status, err)
	}
}

func TestProber_SoftFailure_GitHubRateLimit(t *testing.T) {
	regClient := &mockRegistrationServiceClient{}
	ghClient := &MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return nil, errors.New("HTTP 429: API rate limit exceeded")
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:0"),
		WithTimeout(5*time.Second),
		WithRegistrationClient(regClient),
		WithGitHubClient(ghClient),
		WithHookClient(mockActiveHookClient()),
	)

	start := time.Now()
	res, err := p.Run(context.Background())
	duration := time.Since(start)

	if duration > 2*time.Second {
		t.Errorf("Prober should fail immediately on GitHub error without waiting for timeout, took %v", duration)
	}
	if res.Status != StatusSoftFailure {
		t.Fatalf("expected StatusSoftFailure (2), got %v (err: %v)", res.Status, err)
	}
}

func TestProber_CleanupResilience(t *testing.T) {
	var unregistered bool
	var closedIssueNumber int

	regClient := &mockRegistrationServiceClient{
		unregisterFunc: func(ctx context.Context, in *pb.UnregisterRequest, opts ...grpc.CallOption) (*pb.UnregisterResponse, error) {
			unregistered = true
			return &pb.UnregisterResponse{Success: true}, nil
		},
	}

	issueNumber := 505
	ghClient := &MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			num := issueNumber
			state := "open"
			title := req.GetTitle()
			return &github.Issue{
				Number: &num,
				State:  &state,
				Title:  &title,
			}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			if req.GetState() == "closed" {
				closedIssueNumber = number
			}
			return &github.Issue{}, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:0"),
		WithTimeout(100*time.Millisecond),
		WithRegistrationClient(regClient),
		WithGitHubClient(ghClient),
		WithHookClient(mockActiveHookClient()),
	)

	// Run should timeout (Hard Failure), but deferred cleanup MUST unregister and close the opened issue
	res, _ := p.Run(context.Background())
	if res.Status != StatusHardFailure {
		t.Fatalf("expected StatusHardFailure, got %v", res.Status)
	}
	if !unregistered {
		t.Error("expected Unregister to be called during cleanup")
	}
	if closedIssueNumber != issueNumber {
		t.Errorf("closedIssueNumber = %d, want %d (cleanup must close opened issue)", closedIssueNumber, issueNumber)
	}
}

func TestProber_RegistrationTransportError(t *testing.T) {
	transportErr := errors.New("connection refused: dial tcp 127.0.0.1:50051")
	regClient := &mockRegistrationServiceClient{
		registerFunc: func(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error) {
			return nil, transportErr
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:50000"),
		WithRegistrationClient(regClient),
	)

	res, err := p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from Run, got nil")
	}
	if res.Status != StatusHardFailure {
		t.Fatalf("res.Status = %v, want StatusHardFailure", res.Status)
	}
	expectedPrefix := "registration transport error for brotherlogic/ghwebhook (127.0.0.1:50000):"
	if !strings.Contains(res.Message, expectedPrefix) {
		t.Errorf("res.Message = %q, want substring %q", res.Message, expectedPrefix)
	}
	if !strings.Contains(res.Message, "connection refused") {
		t.Errorf("res.Message = %q, want underlying transport error", res.Message)
	}
	if !errors.Is(err, transportErr) {
		t.Errorf("errors.Is(err, transportErr) = false, want true (error should wrap transport error)")
	}
}

func TestProber_RegistrationRejection(t *testing.T) {
	rejectionReason := "pstore unavailable"
	regClient := &mockRegistrationServiceClient{
		registerFunc: func(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error) {
			return &pb.RegistrationResponse{
				Success: false,
				Message: rejectionReason,
			}, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:50000"),
		WithRegistrationClient(regClient),
	)

	res, err := p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from Run, got nil")
	}
	if res.Status != StatusHardFailure {
		t.Fatalf("res.Status = %v, want StatusHardFailure", res.Status)
	}
	expectedMsg := "registration rejected by ghwebhook for brotherlogic/ghwebhook (127.0.0.1:50000): pstore unavailable"
	if res.Message != expectedMsg {
		t.Errorf("res.Message = %q, want %q", res.Message, expectedMsg)
	}
	if err.Error() != expectedMsg {
		t.Errorf("err.Error() = %q, want %q", err.Error(), expectedMsg)
	}
}

func TestProber_RegistrationRejection_EmptyMessage(t *testing.T) {
	regClient := &mockRegistrationServiceClient{
		registerFunc: func(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error) {
			return &pb.RegistrationResponse{
				Success: false,
				Message: "   ",
			}, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:50000"),
		WithRegistrationClient(regClient),
	)

	res, err := p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from Run, got nil")
	}
	if res.Status != StatusHardFailure {
		t.Fatalf("res.Status = %v, want StatusHardFailure", res.Status)
	}
	expectedMsg := "registration rejected by ghwebhook for brotherlogic/ghwebhook (127.0.0.1:50000): registration rejected by ghwebhook without specific error message"
	if res.Message != expectedMsg {
		t.Errorf("res.Message = %q, want %q", res.Message, expectedMsg)
	}
	if err.Error() != expectedMsg {
		t.Errorf("err.Error() = %q, want %q", err.Error(), expectedMsg)
	}
}

func TestProber_RegistrationNilResponse(t *testing.T) {
	regClient := &mockRegistrationServiceClient{
		registerFunc: func(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error) {
			return nil, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:50000"),
		WithRegistrationClient(regClient),
	)

	res, err := p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from Run, got nil")
	}
	if res.Status != StatusHardFailure {
		t.Fatalf("res.Status = %v, want StatusHardFailure", res.Status)
	}
	expectedMsg := "unexpected nil registration response from ghwebhook for brotherlogic/ghwebhook (127.0.0.1:50000)"
	if res.Message != expectedMsg {
		t.Errorf("res.Message = %q, want %q", res.Message, expectedMsg)
	}
	if err.Error() != expectedMsg {
		t.Errorf("err.Error() = %q, want %q", err.Error(), expectedMsg)
	}
}

func TestIsGitHub422(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "github.ErrorResponse with 422",
			err: &github.ErrorResponse{
				Response: &http.Response{
					StatusCode: http.StatusUnprocessableEntity,
				},
				Message: "Query must include 'is:issue' or 'is:pull-request'",
			},
			want: true,
		},
		{
			name: "wrapped github.ErrorResponse with 422",
			err: fmt.Errorf("search failed: %w", &github.ErrorResponse{
				Response: &http.Response{
					StatusCode: http.StatusUnprocessableEntity,
				},
			}),
			want: true,
		},
		{
			name: "error message containing 422",
			err:  errors.New("GET https://api.github.com/search/issues?q=...: 422 Query must include 'is:issue' or 'is:pull-request' []"),
			want: true,
		},
		{
			name: "github.ErrorResponse with 500",
			err: &github.ErrorResponse{
				Response: &http.Response{
					StatusCode: http.StatusInternalServerError,
				},
			},
			want: false,
		},
		{
			name: "github.ErrorResponse with 429",
			err: &github.ErrorResponse{
				Response: &http.Response{
					StatusCode: http.StatusTooManyRequests,
				},
			},
			want: false,
		},
		{
			name: "generic network error",
			err:  errors.New("connection reset by peer"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isGitHub422(tt.err); got != tt.want {
				t.Errorf("isGitHub422(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestProber_HardFailure_GitHub422(t *testing.T) {
	t.Run("SearchIssues 422 returns StatusHardFailure", func(t *testing.T) {
		regClient := &mockRegistrationServiceClient{}
		ghClient := &MockGitHubIssueClient{
			SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
				return nil, &github.ErrorResponse{
					Response: &http.Response{StatusCode: 422},
					Message:  "Query must include 'is:issue' or 'is:pull-request'",
				}
			},
		}

		p := NewProber(
			WithRepo("brotherlogic/ghwebhook"),
			WithTargetTitle("PROBER TEST"),
			WithListenAddr("127.0.0.1:0"),
			WithServiceAddr("127.0.0.1:0"),
			WithTimeout(5*time.Second),
			WithRegistrationClient(regClient),
			WithGitHubClient(ghClient),
			WithHookClient(mockActiveHookClient()),
		)

		res, err := p.Run(context.Background())
		if err == nil {
			t.Fatal("expected error from Run, got nil")
		}
		if res.Status != StatusHardFailure {
			t.Fatalf("expected StatusHardFailure (1) for 422 error, got %v", res.Status)
		}
	})

	t.Run("CreateIssue 422 returns StatusHardFailure", func(t *testing.T) {
		regClient := &mockRegistrationServiceClient{}
		ghClient := &MockGitHubIssueClient{
			SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
				return []*github.Issue{}, nil
			},
			CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
				return nil, errors.New("422 Validation Failed: Invalid field")
			},
		}

		p := NewProber(
			WithRepo("brotherlogic/ghwebhook"),
			WithTargetTitle("PROBER TEST"),
			WithListenAddr("127.0.0.1:0"),
			WithServiceAddr("127.0.0.1:0"),
			WithTimeout(5*time.Second),
			WithRegistrationClient(regClient),
			WithGitHubClient(ghClient),
			WithHookClient(mockActiveHookClient()),
		)

		res, err := p.Run(context.Background())
		if err == nil {
			t.Fatal("expected error from Run, got nil")
		}
		if res.Status != StatusHardFailure {
			t.Fatalf("expected StatusHardFailure (1) for 422 error, got %v", res.Status)
		}
	})

	t.Run("EditIssue 422 returns StatusHardFailure", func(t *testing.T) {
		num := 99
		state := "open"
		title := "PROBER TEST"

		regClient := &mockRegistrationServiceClient{}
		ghClient := &MockGitHubIssueClient{
			SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
				return []*github.Issue{
					{
						Number: &num,
						State:  &state,
						Title:  &title,
					},
				}, nil
			},
			EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
				return nil, &github.ErrorResponse{
					Response: &http.Response{StatusCode: 422},
					Message:  "Validation Failed",
				}
			},
		}

		p := NewProber(
			WithRepo("brotherlogic/ghwebhook"),
			WithTargetTitle("PROBER TEST"),
			WithListenAddr("127.0.0.1:0"),
			WithServiceAddr("127.0.0.1:0"),
			WithTimeout(5*time.Second),
			WithRegistrationClient(regClient),
			WithGitHubClient(ghClient),
			WithHookClient(mockActiveHookClient()),
		)

		res, err := p.Run(context.Background())
		if err == nil {
			t.Fatal("expected error from Run, got nil")
		}
		if res.Status != StatusHardFailure {
			t.Fatalf("expected StatusHardFailure (1) for 422 error, got %v", res.Status)
		}
	})
}

func TestInspectWebhooks_WebhookMissing(t *testing.T) {
	hookID := int64(100)
	active := true
	inactive := false
	hookClient := &MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			return []*github.Hook{
				{
					ID:     &hookID,
					Active: &inactive,
					Events: []string{"issues"},
				},
				{
					ID:     &hookID,
					Active: &active,
					Events: []string{"push", "pull_request"},
				},
			}, nil
		},
	}

	p := NewProber(WithHookClient(hookClient))
	diag := p.inspectWebhooks(context.Background(), "owner", "repo", time.Now(), "opened")
	if diag == nil {
		t.Fatal("expected non-nil InspectionDiagnostics, got nil")
	}
	if diag.RootCause != RootCauseWebhookMissing {
		t.Errorf("expected RootCause %q, got %q", RootCauseWebhookMissing, diag.RootCause)
	}
	if diag.ActiveHooksCount != 0 {
		t.Errorf("expected ActiveHooksCount 0, got %d", diag.ActiveHooksCount)
	}
}

func TestInspectWebhooks_DeliveryFailed(t *testing.T) {
	hookID := int64(101)
	deliveryID := int64(201)
	active := true
	now := time.Now()
	guid := "delivery-guid-fail"
	event := "issues"
	action := "opened"
	status := "failed"
	statusCode := 502
	duration := 0.25

	hookClient := &MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			return []*github.Hook{
				{
					ID:     &hookID,
					Active: &active,
					Events: []string{"issues"},
				},
			}, nil
		},
		ListHookDeliveriesFunc: func(ctx context.Context, owner, repo string, hID int64, opts *github.ListCursorOptions) ([]*github.HookDelivery, error) {
			return []*github.HookDelivery{
				{
					ID:          &deliveryID,
					GUID:        &guid,
					DeliveredAt: &github.Timestamp{Time: now.Add(1 * time.Second)},
					Duration:    &duration,
					Status:      &status,
					StatusCode:  &statusCode,
					Event:       &event,
					Action:      &action,
				},
			}, nil
		},
	}

	p := NewProber(WithHookClient(hookClient))
	diag := p.inspectWebhooks(context.Background(), "owner", "repo", now, "opened")
	if diag == nil {
		t.Fatal("expected non-nil InspectionDiagnostics, got nil")
	}
	if diag.RootCause != RootCauseDeliveryFailed {
		t.Errorf("expected RootCause %q, got %q", RootCauseDeliveryFailed, diag.RootCause)
	}
	if diag.ActiveHooksCount != 1 {
		t.Errorf("expected ActiveHooksCount 1, got %d", diag.ActiveHooksCount)
	}
	if len(diag.MatchingDeliveries) != 1 {
		t.Fatalf("expected 1 matching delivery, got %d", len(diag.MatchingDeliveries))
	}
	if diag.MatchingDeliveries[0].StatusCode != 502 {
		t.Errorf("expected status code 502, got %d", diag.MatchingDeliveries[0].StatusCode)
	}
}

func TestInspectWebhooks_LostInRouting(t *testing.T) {
	hookID := int64(102)
	deliveryID := int64(202)
	active := true
	now := time.Now()
	guid := "delivery-guid-ok"
	event := "issues"
	action := "reopened"
	status := "OK"
	statusCode := 200
	duration := 0.12

	hookClient := &MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			return []*github.Hook{
				{
					ID:     &hookID,
					Active: &active,
					Events: []string{"*"},
				},
			}, nil
		},
		ListHookDeliveriesFunc: func(ctx context.Context, owner, repo string, hID int64, opts *github.ListCursorOptions) ([]*github.HookDelivery, error) {
			return []*github.HookDelivery{
				{
					ID:          &deliveryID,
					GUID:        &guid,
					DeliveredAt: &github.Timestamp{Time: now.Add(2 * time.Second)},
					Duration:    &duration,
					Status:      &status,
					StatusCode:  &statusCode,
					Event:       &event,
					Action:      &action,
				},
			}, nil
		},
	}

	p := NewProber(WithHookClient(hookClient))
	diag := p.inspectWebhooks(context.Background(), "owner", "repo", now, "reopened")
	if diag == nil {
		t.Fatal("expected non-nil InspectionDiagnostics, got nil")
	}
	if diag.RootCause != RootCauseLostInRouting {
		t.Errorf("expected RootCause %q, got %q", RootCauseLostInRouting, diag.RootCause)
	}
	if diag.ActiveHooksCount != 1 {
		t.Errorf("expected ActiveHooksCount 1, got %d", diag.ActiveHooksCount)
	}
	if len(diag.MatchingDeliveries) != 1 {
		t.Fatalf("expected 1 matching delivery, got %d", len(diag.MatchingDeliveries))
	}
	if diag.MatchingDeliveries[0].StatusCode != 200 {
		t.Errorf("expected status code 200, got %d", diag.MatchingDeliveries[0].StatusCode)
	}
}

func TestInspectWebhooks_NoDeliveryAttempted(t *testing.T) {
	hookID := int64(103)
	deliveryID := int64(203)
	active := true
	now := time.Now()
	guid := "delivery-guid-old"
	event := "issues"
	diffAction := "closed"
	status := "OK"
	statusCode := 200

	hookClient := &MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			return []*github.Hook{
				{
					ID:     &hookID,
					Active: &active,
					Events: []string{"issues"},
				},
			}, nil
		},
		ListHookDeliveriesFunc: func(ctx context.Context, owner, repo string, hID int64, opts *github.ListCursorOptions) ([]*github.HookDelivery, error) {
			return []*github.HookDelivery{
				// Delivery with different action
				{
					ID:          &deliveryID,
					GUID:        &guid,
					DeliveredAt: &github.Timestamp{Time: now.Add(1 * time.Second)},
					Status:      &status,
					StatusCode:  &statusCode,
					Event:       &event,
					Action:      &diffAction,
				},
				// Delivery before cutoff (skew tolerance is 5s)
				{
					ID:          &deliveryID,
					GUID:        &guid,
					DeliveredAt: &github.Timestamp{Time: now.Add(-10 * time.Second)},
					Status:      &status,
					StatusCode:  &statusCode,
					Event:       &event,
					Action:      github.Ptr("opened"),
				},
			}, nil
		},
	}

	p := NewProber(WithHookClient(hookClient))
	diag := p.inspectWebhooks(context.Background(), "owner", "repo", now, "opened")
	if diag == nil {
		t.Fatal("expected non-nil InspectionDiagnostics, got nil")
	}
	if diag.RootCause != RootCauseNoDeliveryAttempted {
		t.Errorf("expected RootCause %q, got %q", RootCauseNoDeliveryAttempted, diag.RootCause)
	}
	if diag.ActiveHooksCount != 1 {
		t.Errorf("expected ActiveHooksCount 1, got %d", diag.ActiveHooksCount)
	}
	if len(diag.MatchingDeliveries) != 0 {
		t.Errorf("expected 0 matching deliveries, got %d", len(diag.MatchingDeliveries))
	}
}

func TestInspectWebhooks_InspectionUnavailable_Permissions(t *testing.T) {
	for _, code := range []int{403, 404} {
		t.Run(fmt.Sprintf("HTTP %d", code), func(t *testing.T) {
			hookClient := &MockGitHubHookClient{
				ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
					return nil, &github.ErrorResponse{
						Response: &http.Response{StatusCode: code},
						Message:  "Must have admin rights to Repository",
					}
				},
			}

			p := NewProber(WithHookClient(hookClient))
			diag := p.inspectWebhooks(context.Background(), "owner", "repo", time.Now(), "opened")
			if diag == nil {
				t.Fatal("expected non-nil InspectionDiagnostics, got nil")
			}
			if diag.RootCause != RootCauseInspectionUnavailable {
				t.Errorf("expected RootCause %q, got %q", RootCauseInspectionUnavailable, diag.RootCause)
			}
			if !strings.Contains(strings.ToLower(diag.RootCauseDetail), "permissions") && !strings.Contains(strings.ToLower(diag.RootCauseDetail), "admin:repo_hook") {
				t.Errorf("expected permission notice in RootCauseDetail, got %q", diag.RootCauseDetail)
			}
		})
	}
}

func TestInspectWebhooks_InspectionUnavailable_GenericError(t *testing.T) {
	hookClient := &MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			return nil, errors.New("500 Internal Server Error")
		},
	}

	p := NewProber(WithHookClient(hookClient))
	diag := p.inspectWebhooks(context.Background(), "owner", "repo", time.Now(), "opened")
	if diag == nil {
		t.Fatal("expected non-nil InspectionDiagnostics, got nil")
	}
	if diag.RootCause != RootCauseInspectionUnavailable {
		t.Errorf("expected RootCause %q, got %q", RootCauseInspectionUnavailable, diag.RootCause)
	}
}

func TestInspectWebhooks_NilHookClient_Fallback(t *testing.T) {
	p := NewProber() // hookClient is nil
	// Calling inspectWebhooks with a canceled context should not panic and should handle error gracefully
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	diag := p.inspectWebhooks(ctx, "owner", "repo", time.Now(), "opened")
	if diag == nil {
		t.Fatal("expected non-nil InspectionDiagnostics, got nil")
	}
	if diag.RootCause != RootCauseInspectionUnavailable {
		t.Errorf("expected RootCause %q, got %q", RootCauseInspectionUnavailable, diag.RootCause)
	}
}

func TestProber_Run_Timeout_WithInspectionDiagnostics(t *testing.T) {
	hookID := int64(888)
	deliveryID := int64(999)
	active := true
	now := time.Now()
	guid := "delivery-run-guid"
	event := "issues"
	action := "closed"
	status := "OK"
	statusCode := 200
	duration := 0.05

	regClient := &mockRegistrationServiceClient{}
	ghClient := &MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			num := 42
			state := "open"
			title := "PROBER TEST"
			return []*github.Issue{{
				Number: &num,
				State:  &state,
				Title:  &title,
			}}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			state := req.GetState()
			return &github.Issue{Number: &number, State: &state}, nil
		},
	}
	hookClient := &MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			return []*github.Hook{{
				ID:     &hookID,
				Active: &active,
				Events: []string{"issues"},
			}}, nil
		},
		ListHookDeliveriesFunc: func(ctx context.Context, owner, repo string, hID int64, opts *github.ListCursorOptions) ([]*github.HookDelivery, error) {
			return []*github.HookDelivery{{
				ID:          &deliveryID,
				GUID:        &guid,
				DeliveredAt: &github.Timestamp{Time: now.Add(100 * time.Millisecond)},
				Duration:    &duration,
				Status:      &status,
				StatusCode:  &statusCode,
				Event:       &event,
				Action:      &action,
			}}, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:0"),
		WithTimeout(100*time.Millisecond),
		WithRegistrationClient(regClient),
		WithGitHubClient(ghClient),
		WithHookClient(hookClient),
	)

	res, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("expected nil error on timeout from Run, got %v", err)
	}
	if res.Status != StatusHardFailure {
		t.Fatalf("expected StatusHardFailure, got %v", res.Status)
	}
	if res.Diagnostics == nil {
		t.Fatal("expected non-nil Diagnostics on timeout, got nil")
	}
	if res.Diagnostics.RootCause != RootCauseLostInRouting {
		t.Errorf("expected RootCause %q, got %q", RootCauseLostInRouting, res.Diagnostics.RootCause)
	}
}

func TestProber_Run_ContextCancelled_SkipsInspection(t *testing.T) {
	regClient := &mockRegistrationServiceClient{}
	ghClient := &MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			num := 42
			state := "open"
			title := "PROBER TEST"
			return []*github.Issue{{
				Number: &num,
				State:  &state,
				Title:  &title,
			}}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			state := req.GetState()
			return &github.Issue{Number: &number, State: &state}, nil
		},
	}
	hookClientCalled := false
	hookClient := &MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			hookClientCalled = true
			return []*github.Hook{}, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:0"),
		WithTimeout(5*time.Second),
		WithRegistrationClient(regClient),
		WithGitHubClient(ghClient),
		WithHookClient(hookClient),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	res, err := p.Run(ctx)
	if err == nil {
		t.Fatal("expected context cancelled error, got nil")
	}
	if hookClientCalled {
		t.Error("expected hookClient NOT to be called when context is cancelled")
	}
	if res.Diagnostics != nil {
		t.Errorf("expected nil Diagnostics when context is cancelled, got %v", res.Diagnostics)
	}
}

func TestRun_FailsFast_WhenWebhookMissingPostRegistration(t *testing.T) {
	var unregistered bool
	regClient := &mockRegistrationServiceClient{
		registerFunc: func(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error) {
			return &pb.RegistrationResponse{Success: true}, nil
		},
		unregisterFunc: func(ctx context.Context, in *pb.UnregisterRequest, opts ...grpc.CallOption) (*pb.UnregisterResponse, error) {
			unregistered = true
			return &pb.UnregisterResponse{Success: true}, nil
		},
	}

	issueAPICalled := false
	ghClient := &MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			issueAPICalled = true
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			issueAPICalled = true
			return nil, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			issueAPICalled = true
			return nil, nil
		},
	}

	hookClient := &MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			return []*github.Hook{}, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:0"),
		WithIngressURL("https://example.com/webhook"),
		WithTimeout(10*time.Second),
		WithRegistrationClient(regClient),
		WithGitHubClient(ghClient),
		WithHookClient(hookClient),
	)

	start := time.Now()
	res, _ := p.Run(context.Background())
	duration := time.Since(start)

	if duration > 2*time.Second {
		t.Errorf("Prober should fail fast without waiting for timeout, took %v", duration)
	}
	if res.Status != StatusHardFailure {
		t.Fatalf("res.Status = %v, want StatusHardFailure", res.Status)
	}
	if res.Diagnostics == nil {
		t.Fatal("expected non-nil Diagnostics on early abort")
	}
	if res.Diagnostics.RootCause != RootCauseWebhookMissing {
		t.Errorf("res.Diagnostics.RootCause = %q, want %q", res.Diagnostics.RootCause, RootCauseWebhookMissing)
	}
	expectedDetail := "no active webhook configured for ghwebhook found on repository following registration"
	if res.Diagnostics.RootCauseDetail != expectedDetail {
		t.Errorf("res.Diagnostics.RootCauseDetail = %q, want %q", res.Diagnostics.RootCauseDetail, expectedDetail)
	}
	if res.Diagnostics.ActiveHooksCount != 0 {
		t.Errorf("res.Diagnostics.ActiveHooksCount = %d, want 0", res.Diagnostics.ActiveHooksCount)
	}
	if issueAPICalled {
		t.Error("expected GitHub issue APIs NOT to be called when webhook is missing")
	}
	if !unregistered {
		t.Error("expected unregister to be executed on early abort")
	}
}

func TestRun_FailsFast_WhenInspectionPermissionDenied(t *testing.T) {
	regClient := &mockRegistrationServiceClient{
		registerFunc: func(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error) {
			return &pb.RegistrationResponse{Success: true}, nil
		},
	}

	issueAPICalled := false
	ghClient := &MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			issueAPICalled = true
			return []*github.Issue{}, nil
		},
	}

	hookClient := &MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			return nil, &github.ErrorResponse{
				Response: &http.Response{
					StatusCode: http.StatusForbidden,
				},
				Message: "Resource not accessible by integration",
			}
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:0"),
		WithTimeout(10*time.Second),
		WithRegistrationClient(regClient),
		WithGitHubClient(ghClient),
		WithHookClient(hookClient),
	)

	start := time.Now()
	res, _ := p.Run(context.Background())
	duration := time.Since(start)

	if duration > 2*time.Second {
		t.Errorf("Prober should fail fast without waiting for timeout, took %v", duration)
	}
	if res.Status != StatusHardFailure {
		t.Fatalf("res.Status = %v, want StatusHardFailure", res.Status)
	}
	if res.Diagnostics == nil {
		t.Fatal("expected non-nil Diagnostics on permission denied abort")
	}
	if res.Diagnostics.RootCause != RootCauseInspectionUnavailable {
		t.Errorf("res.Diagnostics.RootCause = %q, want %q", res.Diagnostics.RootCause, RootCauseInspectionUnavailable)
	}
	expectedDetail := "insufficient token permissions to inspect repository webhooks (admin:repo_hook required)"
	if res.Diagnostics.RootCauseDetail != expectedDetail {
		t.Errorf("res.Diagnostics.RootCauseDetail = %q, want %q", res.Diagnostics.RootCauseDetail, expectedDetail)
	}
	if issueAPICalled {
		t.Error("expected GitHub issue APIs NOT to be called when permission denied")
	}
}

func TestRun_Succeeds_WhenWebhookActive(t *testing.T) {
	hookID := int64(456)
	active := true
	ingressURL := "https://webhook.brotherlogic-infra.net/event"

	regClient := &mockRegistrationServiceClient{
		registerFunc: func(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error) {
			return &pb.RegistrationResponse{Success: true}, nil
		},
		unregisterFunc: func(ctx context.Context, in *pb.UnregisterRequest, opts ...grpc.CallOption) (*pb.UnregisterResponse, error) {
			return &pb.UnregisterResponse{Success: true}, nil
		},
	}

	issueNum := 202
	ghClient := &MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			state := "open"
			title := req.GetTitle()
			num := issueNum
			return &github.Issue{Number: &num, State: &state, Title: &title}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			state := req.GetState()
			return &github.Issue{Number: &number, State: &state}, nil
		},
	}

	hookClient := &MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			return []*github.Hook{
				{
					ID:     &hookID,
					Active: &active,
					Events: []string{"issues"},
					Config: &github.HookConfig{
						URL: github.Ptr(ingressURL),
					},
				},
			}, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:0"),
		WithIngressURL(ingressURL),
		WithTimeout(3*time.Second),
		WithRegistrationClient(regClient),
		WithGitHubClient(ghClient),
		WithHookClient(hookClient),
	)

	go func() {
		time.Sleep(100 * time.Millisecond)
		p.eventCh <- &pb.WebhookEvent{
			Header: &pb.EventHeader{EventType: "issues"},
			Payload: &pb.WebhookEvent_Issue{
				Issue: &pb.IssueEvent{
					Action: "opened",
					Number: int32(issueNum),
					Title:  "PROBER TEST",
					Repository: &pb.Repository{
						FullName: "brotherlogic/ghwebhook",
					},
				},
			},
		}
	}()

	res, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Prober.Run failed: %v", err)
	}
	if res.Status != StatusSuccess {
		t.Fatalf("res.Status = %v, want StatusSuccess", res.Status)
	}
	if res.IssueNumber != issueNum {
		t.Errorf("res.IssueNumber = %d, want %d", res.IssueNumber, issueNum)
	}
}

func TestRun_CleansUpDeferredResources_OnEarlyAbort(t *testing.T) {
	var unregistered bool
	regClient := &mockRegistrationServiceClient{
		registerFunc: func(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error) {
			return &pb.RegistrationResponse{Success: true}, nil
		},
		unregisterFunc: func(ctx context.Context, in *pb.UnregisterRequest, opts ...grpc.CallOption) (*pb.UnregisterResponse, error) {
			unregistered = true
			return &pb.UnregisterResponse{Success: true}, nil
		},
	}

	hookClient := &MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			return []*github.Hook{}, nil
		},
	}

	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
		WithServiceAddr("127.0.0.1:0"),
		WithTimeout(5*time.Second),
		WithRegistrationClient(regClient),
		WithHookClient(hookClient),
	)

	res, _ := p.Run(context.Background())
	if res.Status != StatusHardFailure {
		t.Fatalf("res.Status = %v, want StatusHardFailure", res.Status)
	}
	if !unregistered {
		t.Error("expected regClient.Unregister to be called upon early abort")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.StartGRPCServer(ctx); err != nil {
		t.Errorf("expected prober gRPC server to be stopped and restartable, got err: %v", err)
	}
	p.StopGRPCServer()
}

