package prober_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brotherlogic/ghwebhook/prober"
	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"github.com/brotherlogic/ghwebhook/server"
	pstore_client "github.com/brotherlogic/pstore/client"
	"github.com/google/go-github/v69/github"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// setupGHWebhookServer starts an in-memory ghwebhook gRPC RegistrationService
// and HTTP webhook ingress server for integration tests.
func setupGHWebhookServer(t *testing.T, secret string) (*server.Server, string, *httptest.Server, func()) {
	t.Helper()

	os.Setenv("GH_WEBHOOK_SECRET", secret)

	pstore := pstore_client.GetTestClient()
	srv := server.NewServer(pstore)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create gRPC listener: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterRegistrationServiceServer(grpcServer, srv)

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	httpServer := httptest.NewServer(srv)

	cleanup := func() {
		httpServer.Close()
		grpcServer.GracefulStop()
		_ = lis.Close()
		os.Unsetenv("GH_WEBHOOK_SECRET")
	}

	return srv, lis.Addr().String(), httpServer, cleanup
}

// sendSignedWebhook helper computes HMAC signature and POSTs to ghwebhook HTTP ingress.
func sendSignedWebhook(t *testing.T, httpURL string, secret string, eventType string, payload any) *http.Response {
	t.Helper()

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal webhook payload: %v", err)
	}

	h := hmac.New(sha256.New, []byte(secret))
	h.Write(bodyBytes)
	signature := "sha256=" + hex.EncodeToString(h.Sum(nil))

	req, err := http.NewRequest(http.MethodPost, httpURL+"/webhook", bytes.NewBuffer(bodyBytes))
	if err != nil {
		t.Fatalf("failed to create http request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", eventType)
	req.Header.Set("X-Hub-Signature-256", signature)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to post webhook: %v", err)
	}

	return resp
}

// TestIntegration_EndToEndFlow_Register_WebhookDelivery_Deregister exercises the full
// in-memory integration workflow:
// 1. Prober starts gRPC server and registers with ghwebhook.
// 2. Simulated GitHub webhook is POSTed to ghwebhook HTTP endpoint with valid HMAC signature.
// 3. ghwebhook routes event via gRPC to prober.ReceiveWebhook.
// 4. Prober validates event and dispatches onto EventChannel.
// 5. Prober unregisters and subsequent webhooks are no longer forwarded.
func TestIntegration_EndToEndFlow_Register_WebhookDelivery_Deregister(t *testing.T) {
	secret := "test-secret-e2e-12345"
	_, grpcAddr, httpServer, cleanup := setupGHWebhookServer(t, secret)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Instantiate and start in-memory prober
	p := prober.NewProber(
		prober.WithRepo("brotherlogic/ghwebhook"),
		prober.WithTargetTitle("PROBER TEST"),
		prober.WithTargetIssueNumber(42),
		prober.WithTargetAction("opened"),
		prober.WithListenAddr("127.0.0.1:0"),
	)

	if err := p.StartGRPCServer(ctx); err != nil {
		t.Fatalf("prober StartGRPCServer failed: %v", err)
	}
	defer p.StopGRPCServer()

	proberAddr := p.ListenAddr()

	// Connect to ghwebhook RegistrationService
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect to ghwebhook registration service: %v", err)
	}
	defer conn.Close()

	regClient := pb.NewRegistrationServiceClient(conn)

	// Step 1: Register prober with ghwebhook
	regResp, err := regClient.Register(ctx, &pb.RegistrationRequest{
		RepoFullName:   "brotherlogic/ghwebhook",
		ServiceAddress: proberAddr,
	})
	if err != nil {
		t.Fatalf("Register RPC failed: %v", err)
	}
	if !regResp.GetSuccess() {
		t.Fatalf("Register RPC returned success=false: %s", regResp.GetMessage())
	}

	// Step 2: Post valid signed GitHub webhook payload to ghwebhook
	payload := map[string]any{
		"action": "opened",
		"number": 42,
		"issue": map[string]any{
			"title": "PROBER TEST",
			"body":  "Automated test issue",
			"user": map[string]any{
				"login": "testbot",
			},
		},
		"repository": map[string]any{
			"full_name": "brotherlogic/ghwebhook",
		},
	}

	resp := sendSignedWebhook(t, httpServer.URL, secret, "issue", payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK from webhook ingress, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Step 3 & 4: Verify ghwebhook dispatched event to prober and prober placed it on EventChannel
	select {
	case event := <-p.EventChannel():
		if event == nil {
			t.Fatal("received nil WebhookEvent from EventChannel")
		}
		issue := event.GetIssue()
		if issue == nil {
			t.Fatal("received non-issue event on EventChannel")
		}
		if issue.GetNumber() != 42 {
			t.Errorf("issue number = %d, want 42", issue.GetNumber())
		}
		if issue.GetAction() != "opened" {
			t.Errorf("issue action = %q, want 'opened'", issue.GetAction())
		}
		if issue.GetTitle() != "PROBER TEST" {
			t.Errorf("issue title = %q, want 'PROBER TEST'", issue.GetTitle())
		}
		if issue.GetRepository().GetFullName() != "brotherlogic/ghwebhook" {
			t.Errorf("issue repository = %q, want 'brotherlogic/ghwebhook'", issue.GetRepository().GetFullName())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event on prober EventChannel")
	}

	// Step 5: Unregister prober from ghwebhook
	unregResp, err := regClient.Unregister(ctx, &pb.UnregisterRequest{
		RepoFullName:   "brotherlogic/ghwebhook",
		ServiceAddress: proberAddr,
	})
	if err != nil {
		t.Fatalf("Unregister RPC failed: %v", err)
	}
	if !unregResp.GetSuccess() {
		t.Fatalf("Unregister RPC returned success=false: %s", unregResp.GetMessage())
	}

	// Post another webhook and verify it is NO LONGER forwarded to prober
	secondPayload := map[string]any{
		"action": "opened",
		"number": 43,
		"issue": map[string]any{
			"title": "PROBER TEST",
			"body":  "Second issue",
			"user": map[string]any{
				"login": "testbot",
			},
		},
		"repository": map[string]any{
			"full_name": "brotherlogic/ghwebhook",
		},
	}

	resp2 := sendSignedWebhook(t, httpServer.URL, secret, "issue", secondPayload)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK from webhook ingress, got %d", resp2.StatusCode)
	}
	_ = resp2.Body.Close()

	select {
	case ev := <-p.EventChannel():
		t.Fatalf("expected no events after unregistration, but received event for issue #%d", ev.GetIssue().GetNumber())
	case <-time.After(300 * time.Millisecond):
		// Success: no event delivered to unregistered prober
	}
}

// TestIntegration_ProberRun_FullLifecycle tests the complete prober.Run lifecycle
// exercising registration, GitHub issue mutation, incoming webhook dispatch, validation,
// and automatic deferred cleanup.
func TestIntegration_ProberRun_FullLifecycle(t *testing.T) {
	secret := "test-secret-full-run-12345"
	_, grpcAddr, httpServer, cleanup := setupGHWebhookServer(t, secret)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	issueNumber := 999
	issueTitle := "PROBER TEST"
	var createdTitle string
	var issueClosed bool

	mockGHClient := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{}, nil // No existing issue -> triggers CreateIssue
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			createdTitle = req.GetTitle()
			num := issueNumber
			state := "open"
			title := req.GetTitle()

			// Asynchronously simulate GitHub firing webhook to ghwebhook HTTP ingress
			go func() {
				time.Sleep(50 * time.Millisecond)
				payload := map[string]any{
					"action": "opened",
					"number": num,
					"issue": map[string]any{
						"title": title,
						"body":  "Automated issue",
						"user": map[string]any{
							"login": "prober-bot",
						},
					},
					"repository": map[string]any{
						"full_name": fmt.Sprintf("%s/%s", owner, repo),
					},
				}
				resp := sendSignedWebhook(t, httpServer.URL, secret, "issue", payload)
				_ = resp.Body.Close()
			}()

			return &github.Issue{
				Number: &num,
				State:  &state,
				Title:  &title,
			}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			if req.GetState() == "closed" {
				issueClosed = true
			}
			state := req.GetState()
			return &github.Issue{Number: &number, State: &state}, nil
		},
	}

	p := prober.NewProber(
		prober.WithGHWebhookAddr(grpcAddr),
		prober.WithListenAddr("127.0.0.1:0"),
		prober.WithServiceAddr("127.0.0.1:0"),
		prober.WithRepo("brotherlogic/ghwebhook"),
		prober.WithTargetTitle(issueTitle),
		prober.WithGitHubClient(mockGHClient),
		prober.WithTimeout(3*time.Second),
	)

	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("prober.Run failed: %v", err)
	}

	if result.Status != prober.StatusSuccess {
		t.Errorf("result.Status = %v, want StatusSuccess (%s)", result.Status, result.Message)
	}
	if result.IssueNumber != issueNumber {
		t.Errorf("result.IssueNumber = %d, want %d", result.IssueNumber, issueNumber)
	}
	if result.Action != "opened" {
		t.Errorf("result.Action = %q, want 'opened'", result.Action)
	}
	if createdTitle != issueTitle {
		t.Errorf("createdTitle = %q, want %q", createdTitle, issueTitle)
	}
	if !issueClosed {
		t.Error("expected test issue to be closed during deferred cleanup")
	}
}

// TestIntegration_ProberRun_ReopenedIssueFlow tests prober.Run when an existing issue
// is in closed state, validating that it reopens the issue and matches the 'reopened' webhook.
func TestIntegration_ProberRun_ReopenedIssueFlow(t *testing.T) {
	secret := "test-secret-reopen-12345"
	_, grpcAddr, httpServer, cleanup := setupGHWebhookServer(t, secret)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	existingNum := 555
	existingTitle := "PROBER TEST"
	closedState := "closed"
	var reopenedCalled bool
	var issueClosedAtEnd bool

	mockGHClient := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{
				{
					Number: &existingNum,
					Title:  &existingTitle,
					State:  &closedState,
				},
			}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			if req.GetState() == "open" {
				reopenedCalled = true
				// Asynchronously simulate GitHub firing 'reopened' webhook to ghwebhook
				go func() {
					time.Sleep(50 * time.Millisecond)
					payload := map[string]any{
						"action": "reopened",
						"number": number,
						"issue": map[string]any{
							"title": existingTitle,
							"body":  "Reopened issue",
							"user": map[string]any{
								"login": "prober-bot",
							},
						},
						"repository": map[string]any{
							"full_name": fmt.Sprintf("%s/%s", owner, repo),
						},
					}
					resp := sendSignedWebhook(t, httpServer.URL, secret, "issue", payload)
					_ = resp.Body.Close()
				}()
			} else if req.GetState() == "closed" {
				issueClosedAtEnd = true
			}
			state := req.GetState()
			return &github.Issue{Number: &number, State: &state}, nil
		},
	}

	p := prober.NewProber(
		prober.WithGHWebhookAddr(grpcAddr),
		prober.WithListenAddr("127.0.0.1:0"),
		prober.WithServiceAddr("127.0.0.1:0"),
		prober.WithRepo("brotherlogic/ghwebhook"),
		prober.WithTargetTitle(existingTitle),
		prober.WithGitHubClient(mockGHClient),
		prober.WithTimeout(3*time.Second),
	)

	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("prober.Run failed: %v", err)
	}

	if result.Status != prober.StatusSuccess {
		t.Errorf("result.Status = %v, want StatusSuccess (%s)", result.Status, result.Message)
	}
	if result.Action != "reopened" {
		t.Errorf("result.Action = %q, want 'reopened'", result.Action)
	}
	if !reopenedCalled {
		t.Error("expected EditIssue to be called with state='open'")
	}
	if !issueClosedAtEnd {
		t.Error("expected test issue to be closed in cleanup")
	}
}

// TestIntegration_ProberRun_TimeoutBehavior tests prober.Run when no webhook event
// arrives, ensuring it returns StatusHardFailure and performs clean deregistration.
func TestIntegration_ProberRun_TimeoutBehavior(t *testing.T) {
	secret := "test-secret-timeout-12345"
	_, grpcAddr, _, cleanup := setupGHWebhookServer(t, secret)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	issueNum := 777
	title := "PROBER TEST"
	var issueClosed bool

	mockGHClient := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			num := issueNum
			state := "open"
			t := req.GetTitle()
			// Intentionally DO NOT send any webhook event to trigger timeout
			return &github.Issue{Number: &num, State: &state, Title: &t}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			if req.GetState() == "closed" {
				issueClosed = true
			}
			state := req.GetState()
			return &github.Issue{Number: &number, State: &state}, nil
		},
	}

	p := prober.NewProber(
		prober.WithGHWebhookAddr(grpcAddr),
		prober.WithListenAddr("127.0.0.1:0"),
		prober.WithServiceAddr("127.0.0.1:0"),
		prober.WithRepo("brotherlogic/ghwebhook"),
		prober.WithTargetTitle(title),
		prober.WithGitHubClient(mockGHClient),
		prober.WithTimeout(100*time.Millisecond),
	)

	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("expected nil err on timeout result return, got %v", err)
	}

	if result.Status != prober.StatusHardFailure {
		t.Errorf("result.Status = %v, want StatusHardFailure", result.Status)
	}
	if !strings.Contains(result.Message, "timed out") {
		t.Errorf("result.Message = %q, want to contain 'timed out'", result.Message)
	}
	if !issueClosed {
		t.Error("expected open test issue to be closed after timeout cleanup")
	}
}

// TestIntegration_DeregistrationIdempotency tests that calling Unregister multiple times
// or unregistering unknown services behaves gracefully without errors.
func TestIntegration_DeregistrationIdempotency(t *testing.T) {
	secret := "test-secret-idempotency-12345"
	_, grpcAddr, _, cleanup := setupGHWebhookServer(t, secret)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial ghwebhook: %v", err)
	}
	defer conn.Close()

	regClient := pb.NewRegistrationServiceClient(conn)

	repo := "brotherlogic/ghwebhook"
	svcAddr := "127.0.0.1:55555"

	// Initial register
	regResp, err := regClient.Register(ctx, &pb.RegistrationRequest{
		RepoFullName:   repo,
		ServiceAddress: svcAddr,
	})
	if err != nil || !regResp.GetSuccess() {
		t.Fatalf("initial register failed: %v, resp: %v", err, regResp)
	}

	// 1. First unregister -> should succeed
	unregResp1, err := regClient.Unregister(ctx, &pb.UnregisterRequest{
		RepoFullName:   repo,
		ServiceAddress: svcAddr,
	})
	if err != nil {
		t.Fatalf("first unregister failed: %v", err)
	}
	if !unregResp1.GetSuccess() {
		t.Errorf("first unregister returned success=false: %s", unregResp1.GetMessage())
	}

	// 2. Second unregister for same service (idempotency check) -> verifies it returns NotFound code
	unregResp2, err := regClient.Unregister(ctx, &pb.UnregisterRequest{
		RepoFullName:   repo,
		ServiceAddress: svcAddr,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound code for second unregister, got err: %v, resp: %v", err, unregResp2)
	}
}

// TestIntegration_ProberRun_ClosedIssueFlow tests prober.Run when an existing issue
// is in open state, verifying that it closes the issue and matches the 'closed' webhook.
func TestIntegration_ProberRun_ClosedIssueFlow(t *testing.T) {
	secret := "test-secret-closed-12345"
	_, grpcAddr, httpServer, cleanup := setupGHWebhookServer(t, secret)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	existingNum := 333
	existingTitle := "PROBER TEST"
	openState := "open"
	var closedCalled bool

	mockGHClient := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{
				{
					Number: &existingNum,
					Title:  &existingTitle,
					State:  &openState,
				},
			}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			if req.GetState() == "closed" {
				closedCalled = true
				go func() {
					time.Sleep(50 * time.Millisecond)
					payload := map[string]any{
						"action": "closed",
						"number": number,
						"issue": map[string]any{
							"title": existingTitle,
							"body":  "Closing issue",
							"user": map[string]any{
								"login": "prober-bot",
							},
						},
						"repository": map[string]any{
							"full_name": fmt.Sprintf("%s/%s", owner, repo),
						},
					}
					resp := sendSignedWebhook(t, httpServer.URL, secret, "issue", payload)
					_ = resp.Body.Close()
				}()
			}
			state := req.GetState()
			return &github.Issue{Number: &number, State: &state}, nil
		},
	}

	p := prober.NewProber(
		prober.WithGHWebhookAddr(grpcAddr),
		prober.WithListenAddr("127.0.0.1:0"),
		prober.WithServiceAddr("127.0.0.1:0"),
		prober.WithRepo("brotherlogic/ghwebhook"),
		prober.WithTargetTitle(existingTitle),
		prober.WithGitHubClient(mockGHClient),
		prober.WithTimeout(3*time.Second),
	)

	result, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("prober.Run failed: %v", err)
	}

	if result.Status != prober.StatusSuccess {
		t.Errorf("result.Status = %v, want StatusSuccess (%s)", result.Status, result.Message)
	}
	if result.Action != "closed" {
		t.Errorf("result.Action = %q, want 'closed'", result.Action)
	}
	if !closedCalled {
		t.Error("expected EditIssue to be called with state='closed'")
	}
}

// TestIntegration_MultipleProbers_SelectiveRouting verifies that ghwebhook selectively routes
// webhook events only to probers registered for the matching repository.
func TestIntegration_MultipleProbers_SelectiveRouting(t *testing.T) {
	secret := "test-secret-multi-12345"
	_, grpcAddr, httpServer, cleanup := setupGHWebhookServer(t, secret)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial ghwebhook: %v", err)
	}
	defer conn.Close()

	regClient := pb.NewRegistrationServiceClient(conn)

	// Prober 1 for repoA
	p1 := prober.NewProber(
		prober.WithRepo("brotherlogic/ghwebhook"),
		prober.WithTargetTitle("PROBER TEST"),
		prober.WithListenAddr("127.0.0.1:0"),
	)
	if err := p1.StartGRPCServer(ctx); err != nil {
		t.Fatalf("p1 StartGRPCServer failed: %v", err)
	}
	defer p1.StopGRPCServer()

	// Prober 2 for repoB
	p2 := prober.NewProber(
		prober.WithRepo("other/repo"),
		prober.WithTargetTitle("PROBER TEST"),
		prober.WithListenAddr("127.0.0.1:0"),
	)
	if err := p2.StartGRPCServer(ctx); err != nil {
		t.Fatalf("p2 StartGRPCServer failed: %v", err)
	}
	defer p2.StopGRPCServer()

	_, err = regClient.Register(ctx, &pb.RegistrationRequest{
		RepoFullName:   "brotherlogic/ghwebhook",
		ServiceAddress: p1.ListenAddr(),
	})
	if err != nil {
		t.Fatalf("p1 registration failed: %v", err)
	}

	_, err = regClient.Register(ctx, &pb.RegistrationRequest{
		RepoFullName:   "other/repo",
		ServiceAddress: p2.ListenAddr(),
	})
	if err != nil {
		t.Fatalf("p2 registration failed: %v", err)
	}

	// Send webhook for brotherlogic/ghwebhook
	payload1 := map[string]any{
		"action": "opened",
		"number": 101,
		"issue": map[string]any{
			"title": "PROBER TEST",
			"body":  "Test payload",
			"user":  map[string]any{"login": "bot"},
		},
		"repository": map[string]any{
			"full_name": "brotherlogic/ghwebhook",
		},
	}
	resp1 := sendSignedWebhook(t, httpServer.URL, secret, "issue", payload1)
	_ = resp1.Body.Close()

	// p1 should receive the event
	select {
	case ev := <-p1.EventChannel():
		if ev.GetIssue().GetNumber() != 101 {
			t.Errorf("p1 received issue %d, want 101", ev.GetIssue().GetNumber())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for p1 to receive event")
	}

	// p2 should NOT receive the event
	select {
	case ev := <-p2.EventChannel():
		t.Fatalf("p2 received unexpected event: %v", ev)
	case <-time.After(200 * time.Millisecond):
		// Expected
	}
}

// TestIntegration_ProberRun_SoftFailure_GitHubAPIError tests prober.Run handling of
// upstream GitHub API failures (such as rate limits).
func TestIntegration_ProberRun_SoftFailure_GitHubAPIError(t *testing.T) {
	secret := "test-secret-softfail-12345"
	_, grpcAddr, _, cleanup := setupGHWebhookServer(t, secret)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockGHClient := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return nil, fmt.Errorf("GET https://api.github.com/search/issues: 403 API rate limit exceeded")
		},
	}

	p := prober.NewProber(
		prober.WithGHWebhookAddr(grpcAddr),
		prober.WithListenAddr("127.0.0.1:0"),
		prober.WithServiceAddr("127.0.0.1:0"),
		prober.WithRepo("brotherlogic/ghwebhook"),
		prober.WithTargetTitle("PROBER TEST"),
		prober.WithGitHubClient(mockGHClient),
		prober.WithTimeout(1*time.Second),
	)

	result, err := p.Run(ctx)
	if err == nil {
		t.Fatal("expected error on GitHub API failure, got nil")
	}

	if result.Status != prober.StatusSoftFailure {
		t.Errorf("result.Status = %v, want StatusSoftFailure", result.Status)
	}
	if !strings.Contains(result.Message, "rate limit exceeded") {
		t.Errorf("result.Message = %q, want to mention rate limit", result.Message)
	}
}

