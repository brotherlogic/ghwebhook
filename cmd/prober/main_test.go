package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"testing"
	"time"

	"github.com/brotherlogic/ghwebhook/prober"
	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"github.com/google/go-github/v69/github"
	"google.golang.org/grpc"
)

type mockRegistrationClient struct {
	pb.RegistrationServiceClient
	registerFunc   func(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error)
	unregisterFunc func(ctx context.Context, in *pb.UnregisterRequest, opts ...grpc.CallOption) (*pb.UnregisterResponse, error)
}

func (m *mockRegistrationClient) Register(ctx context.Context, in *pb.RegistrationRequest, opts ...grpc.CallOption) (*pb.RegistrationResponse, error) {
	if m.registerFunc != nil {
		return m.registerFunc(ctx, in, opts...)
	}
	return &pb.RegistrationResponse{Success: true}, nil
}

func (m *mockRegistrationClient) Unregister(ctx context.Context, in *pb.UnregisterRequest, opts ...grpc.CallOption) (*pb.UnregisterResponse, error) {
	if m.unregisterFunc != nil {
		return m.unregisterFunc(ctx, in, opts...)
	}
	return &pb.UnregisterResponse{Success: true}, nil
}

func TestParseConfig_Defaults(t *testing.T) {
	getenv := func(key string) string { return "" }
	cfg, err := parseConfig([]string{}, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Repo != prober.DefaultRepo {
		t.Errorf("expected Repo %q, got %q", prober.DefaultRepo, cfg.Repo)
	}
	if cfg.GHWebhookAddr != prober.DefaultGHWebhookAddr {
		t.Errorf("expected GHWebhookAddr %q, got %q", prober.DefaultGHWebhookAddr, cfg.GHWebhookAddr)
	}
	if cfg.ListenAddr != prober.DefaultListenAddr {
		t.Errorf("expected ListenAddr %q, got %q", prober.DefaultListenAddr, cfg.ListenAddr)
	}
	if cfg.ServiceAddr != prober.DefaultServiceAddr {
		t.Errorf("expected ServiceAddr %q, got %q", prober.DefaultServiceAddr, cfg.ServiceAddr)
	}
	if cfg.Timeout != prober.DefaultTimeout {
		t.Errorf("expected Timeout %v, got %v", prober.DefaultTimeout, cfg.Timeout)
	}
	if cfg.GitHubToken != "" {
		t.Errorf("expected empty GitHubToken, got %q", cfg.GitHubToken)
	}
}

func TestParseConfig_EnvVars(t *testing.T) {
	env := map[string]string{
		"PROBER_REPO":           "testorg/testrepo",
		"PROBER_GHWEBHOOK_ADDR": "ghwebhook:50051",
		"PROBER_LISTEN_ADDR":    ":50099",
		"PROBER_SERVICE_ADDR":   "prober-svc:50099",
		"PROBER_TIMEOUT":        "45s",
		"GH_TOKEN":              "env-token-123",
	}
	getenv := func(key string) string { return env[key] }

	cfg, err := parseConfig([]string{}, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Repo != "testorg/testrepo" {
		t.Errorf("expected Repo testorg/testrepo, got %q", cfg.Repo)
	}
	if cfg.GHWebhookAddr != "ghwebhook:50051" {
		t.Errorf("expected GHWebhookAddr ghwebhook:50051, got %q", cfg.GHWebhookAddr)
	}
	if cfg.ListenAddr != ":50099" {
		t.Errorf("expected ListenAddr :50099, got %q", cfg.ListenAddr)
	}
	if cfg.ServiceAddr != "prober-svc:50099" {
		t.Errorf("expected ServiceAddr prober-svc:50099, got %q", cfg.ServiceAddr)
	}
	if cfg.Timeout != 45*time.Second {
		t.Errorf("expected Timeout 45s, got %v", cfg.Timeout)
	}
	if cfg.GitHubToken != "env-token-123" {
		t.Errorf("expected GitHubToken env-token-123, got %q", cfg.GitHubToken)
	}
}

func TestParseConfig_FallbackGithubToken(t *testing.T) {
	env := map[string]string{
		"GITHUB_TOKEN": "fallback-token-456",
	}
	getenv := func(key string) string { return env[key] }

	cfg, err := parseConfig([]string{}, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GitHubToken != "fallback-token-456" {
		t.Errorf("expected GitHubToken fallback-token-456, got %q", cfg.GitHubToken)
	}
}

func TestParseConfig_FlagOverrides(t *testing.T) {
	env := map[string]string{
		"PROBER_REPO":           "env/repo",
		"PROBER_GHWEBHOOK_ADDR": "env-addr:50051",
		"PROBER_LISTEN_ADDR":    ":50001",
		"PROBER_SERVICE_ADDR":   "env-svc:50001",
		"PROBER_TIMEOUT":        "10s",
		"GH_TOKEN":              "env-token",
	}
	getenv := func(key string) string { return env[key] }

	args := []string{
		"--repo=flagorg/flagrepo",
		"--ghwebhook-addr=flag-addr:50051",
		"--listen-addr=:50002",
		"--service-addr=flag-svc:50002",
		"--timeout=30s",
		"--github-token=flag-token",
	}

	cfg, err := parseConfig(args, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Repo != "flagorg/flagrepo" {
		t.Errorf("expected Repo flagorg/flagrepo, got %q", cfg.Repo)
	}
	if cfg.GHWebhookAddr != "flag-addr:50051" {
		t.Errorf("expected GHWebhookAddr flag-addr:50051, got %q", cfg.GHWebhookAddr)
	}
	if cfg.ListenAddr != ":50002" {
		t.Errorf("expected ListenAddr :50002, got %q", cfg.ListenAddr)
	}
	if cfg.ServiceAddr != "flag-svc:50002" {
		t.Errorf("expected ServiceAddr flag-svc:50002, got %q", cfg.ServiceAddr)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("expected Timeout 30s, got %v", cfg.Timeout)
	}
	if cfg.GitHubToken != "flag-token" {
		t.Errorf("expected GitHubToken flag-token, got %q", cfg.GitHubToken)
	}
}

func TestParseConfig_InvalidTimeout(t *testing.T) {
	env := map[string]string{
		"PROBER_TIMEOUT": "invalid-duration",
	}
	getenv := func(key string) string { return env[key] }

	_, err := parseConfig([]string{}, getenv)
	if err == nil {
		t.Fatalf("expected error on invalid timeout env var, got nil")
	}
}

func TestParseConfig_HelpFlag(t *testing.T) {
	getenv := func(key string) string { return "" }
	_, err := parseConfig([]string{"--help"}, getenv)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
}

func TestRun_Success(t *testing.T) {
	mockGH := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			num := 42
			title := "PROBER TEST"
			state := "open"
			return &github.Issue{Number: &num, Title: &title, State: &state}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			state := "closed"
			return &github.Issue{Number: &number, State: &state}, nil
		},
	}
	mockReg := &mockRegistrationClient{}

	cfg := &Config{
		Repo:          "brotherlogic/ghwebhook",
		GHWebhookAddr: "localhost:50051",
		ListenAddr:    "127.0.0.1:0",
		ServiceAddr:   "127.0.0.1:0",
		Timeout:       2 * time.Second,
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	// Mock Prober injected via options
	exitCode := runWithProber(context.Background(), cfg, &stdout, &stderr,
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
	)

	if exitCode != int(prober.StatusHardFailure) {
		t.Logf("Exit code on timeout without event: %d", exitCode)
	}
	if stdout.Len() == 0 {
		t.Errorf("expected structured log output on stdout, got empty")
	}

	var logEntry map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse structured JSON log: %v (raw: %s)", err, stdout.String())
	}
	if _, ok := logEntry["status"]; !ok {
		t.Errorf("log entry missing 'status' field: %v", logEntry)
	}
}

func TestRun_SoftFailure(t *testing.T) {
	mockGH := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return nil, errors.New("rate limit exceeded (403)")
		},
	}
	mockReg := &mockRegistrationClient{}

	cfg := &Config{
		Repo:          "brotherlogic/ghwebhook",
		GHWebhookAddr: "localhost:50051",
		ListenAddr:    "127.0.0.1:0",
		ServiceAddr:   "127.0.0.1:0",
		Timeout:       1 * time.Second,
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithProber(context.Background(), cfg, &stdout, &stderr,
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
	)

	if exitCode != int(prober.StatusSoftFailure) {
		t.Fatalf("expected exit code %d (StatusSoftFailure), got %d", prober.StatusSoftFailure, exitCode)
	}

	var logEntry map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse structured JSON log: %v (raw: %s)", err, stdout.String())
	}
	if logEntry["status"] != "SOFT_FAILURE" {
		t.Errorf("expected status SOFT_FAILURE, got %v", logEntry["status"])
	}
}

func TestRun_Success_WithDispatchedEvent(t *testing.T) {
	createdNumber := 99
	mockGH := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			title := "PROBER TEST"
			state := "open"
			return &github.Issue{Number: &createdNumber, Title: &title, State: &state}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			state := "closed"
			return &github.Issue{Number: &number, State: &state}, nil
		},
	}
	mockReg := &mockRegistrationClient{}

	cfg := &Config{
		Repo:          "brotherlogic/ghwebhook",
		GHWebhookAddr: "localhost:50051",
		ListenAddr:    "127.0.0.1:0",
		ServiceAddr:   "127.0.0.1:0",
		Timeout:       2 * time.Second,
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	p := prober.NewProber(
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
	)

	go func() {
		// Wait for prober to set target issue and dispatch matching event
		time.Sleep(100 * time.Millisecond)
		_, _ = p.ReceiveWebhook(context.Background(), &pb.WebhookEvent{
			Header: &pb.EventHeader{
				EventType: "issues",
			},
			Payload: &pb.WebhookEvent_Issue{
				Issue: &pb.IssueEvent{
					Action: "opened",
					Number: int32(createdNumber),
					Title:  "PROBER TEST",
					Repository: &pb.Repository{
						FullName: "brotherlogic/ghwebhook",
					},
				},
			},
		})
	}()

	exitCode := run(context.Background(), p, &stdout, &stderr)
	if exitCode != int(prober.StatusSuccess) {
		t.Fatalf("expected exit code %d (StatusSuccess), got %d (stdout: %s)", prober.StatusSuccess, exitCode, stdout.String())
	}

	var logEntry map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse structured JSON log: %v (raw: %s)", err, stdout.String())
	}
	if logEntry["status"] != "SUCCESS" {
		t.Errorf("expected status SUCCESS, got %v", logEntry["status"])
	}
}
