package prober

import (
	"context"
	"errors"
	"net"
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
