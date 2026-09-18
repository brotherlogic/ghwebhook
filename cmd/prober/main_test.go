package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brotherlogic/ghwebhook/prober"
	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"github.com/google/go-github/v69/github"
	"google.golang.org/grpc"
)

var (
	origRecordResult             = prober.RecordResult
	origServeMetricsUntilScraped = prober.ServeMetricsUntilScraped
	origNewDefaultGitHubHookClient = newDefaultGitHubHookClient
)

func defaultTestHookClient() prober.GitHubHookClient {
	hookID := int64(1)
	active := true
	return &prober.MockGitHubHookClient{
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

func TestMain(m *testing.M) {
	serveMetricsUntilScrapedFunc = func(ctx context.Context, addr string, timeout time.Duration) error {
		return nil
	}
	newDefaultGitHubHookClient = func(token string) prober.GitHubHookClient {
		return defaultTestHookClient()
	}
	code := m.Run()
	serveMetricsUntilScrapedFunc = origServeMetricsUntilScraped
	recordResultFunc = origRecordResult
	newDefaultGitHubHookClient = origNewDefaultGitHubHookClient
	os.Exit(code)
}

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

func TestParseConfig_MissingToken_Error(t *testing.T) {
	getenv := func(key string) string { return "" }
	_, err := parseConfig([]string{}, getenv)
	if err == nil {
		t.Fatalf("expected error when GitHub token is missing, got nil")
	}
}

func TestParseConfig_Defaults(t *testing.T) {
	getenv := func(key string) string {
		if key == "GH_TOKEN" {
			return "default-test-token"
		}
		return ""
	}
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
	if cfg.GitHubToken != "default-test-token" {
		t.Errorf("expected GitHubToken default-test-token, got %q", cfg.GitHubToken)
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

	decoder := json.NewDecoder(&stdout)
	var logEntry map[string]interface{}
	if err := decoder.Decode(&logEntry); err != nil {
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

	if exitCode != 0 {
		t.Fatalf("expected exit code 0 on soft failure, got %d", exitCode)
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
		prober.WithHookClient(defaultTestHookClient()),
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

	exitCode := run(context.Background(), cfg, p, mockGH, cfg.Repo, &stdout, &stderr)
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

func TestRun_Success_ExitZero(t *testing.T) {
	createdNumber := 101
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

	p := prober.NewProber(
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
		prober.WithHookClient(defaultTestHookClient()),
	)

	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = p.ReceiveWebhook(context.Background(), &pb.WebhookEvent{
			Header: &pb.EventHeader{EventType: "issues"},
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

	var stdout, stderr bytes.Buffer
	exitCode := run(context.Background(), cfg, p, mockGH, cfg.Repo, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 on success, got %d", exitCode)
	}

	var logEntry map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse JSON log: %v", err)
	}
	if logEntry["status"] != "SUCCESS" {
		t.Errorf("expected status SUCCESS, got %v", logEntry["status"])
	}
}

func TestRun_SoftFailure_ExitZero(t *testing.T) {
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

	p := prober.NewProber(
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
		prober.WithHookClient(defaultTestHookClient()),
	)

	var stdout, stderr bytes.Buffer
	exitCode := run(context.Background(), cfg, p, mockGH, cfg.Repo, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 on soft failure, got %d", exitCode)
	}

	var logEntry map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse JSON log: %v", err)
	}
	if logEntry["status"] != "SOFT_FAILURE" {
		t.Errorf("expected status SOFT_FAILURE, got %v", logEntry["status"])
	}
}

func TestRun_HardFailure_AlertCreated_ExitZero(t *testing.T) {
	createdAlert := false
	alertIssueNum := 555
	alertURL := "https://github.com/brotherlogic/ghwebhook/issues/555"

	createdNumber := 201
	mockGH := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			if req.GetTitle() == prober.DefaultAlertTitle {
				createdAlert = true
				return &github.Issue{
					Number:  &alertIssueNum,
					HTMLURL: &alertURL,
					Title:   github.Ptr(prober.DefaultAlertTitle),
					State:   github.Ptr("open"),
				}, nil
			}
			return &github.Issue{
				Number: &createdNumber,
				Title:  github.Ptr("PROBER TEST"),
				State:  github.Ptr("open"),
			}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			return &github.Issue{Number: &number, State: github.Ptr("closed")}, nil
		},
	}
	mockReg := &mockRegistrationClient{}

	cfg := &Config{
		Repo:          "brotherlogic/ghwebhook",
		GHWebhookAddr: "localhost:50051",
		ListenAddr:    "127.0.0.1:0",
		ServiceAddr:   "127.0.0.1:0",
		Timeout:       50 * time.Millisecond,
	}

	p := prober.NewProber(
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
		prober.WithHookClient(defaultTestHookClient()),
	)

	var stdout, stderr bytes.Buffer
	exitCode := run(context.Background(), cfg, p, mockGH, cfg.Repo, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 on hard failure when alert is created, got %d", exitCode)
	}

	if !createdAlert {
		t.Fatalf("expected alert issue to be created, but CreateIssue was not called for alert")
	}
}

func TestRun_HardFailure_AlertDeduplicated_ExitZero(t *testing.T) {
	existingAlertNum := 777
	existingAlertURL := "https://github.com/brotherlogic/ghwebhook/issues/777"
	createdAlert := false

	createdNumber := 202
	mockGH := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			if strings.Contains(query, prober.DefaultAlertTitle) {
				return []*github.Issue{
					{
						Number:  &existingAlertNum,
						HTMLURL: &existingAlertURL,
						Title:   github.Ptr(prober.DefaultAlertTitle),
						State:   github.Ptr("open"),
					},
				}, nil
			}
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			if req.GetTitle() == prober.DefaultAlertTitle {
				createdAlert = true
			}
			return &github.Issue{
				Number: &createdNumber,
				Title:  github.Ptr("PROBER TEST"),
				State:  github.Ptr("open"),
			}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			return &github.Issue{Number: &number, State: github.Ptr("closed")}, nil
		},
	}
	mockReg := &mockRegistrationClient{}

	cfg := &Config{
		Repo:          "brotherlogic/ghwebhook",
		GHWebhookAddr: "localhost:50051",
		ListenAddr:    "127.0.0.1:0",
		ServiceAddr:   "127.0.0.1:0",
		Timeout:       50 * time.Millisecond,
	}

	p := prober.NewProber(
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
		prober.WithHookClient(defaultTestHookClient()),
	)

	var stdout, stderr bytes.Buffer
	exitCode := run(context.Background(), cfg, p, mockGH, cfg.Repo, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 on hard failure when alert is deduplicated, got %d", exitCode)
	}

	if createdAlert {
		t.Fatalf("expected alert issue NOT to be created when deduplicated, but it was created")
	}
}

func TestRun_HardFailure_AlertFailed_ExitOne(t *testing.T) {
	createdNumber := 203
	mockGH := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			if strings.Contains(query, prober.DefaultAlertTitle) {
				return nil, errors.New("github API outage during search")
			}
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			return &github.Issue{
				Number: &createdNumber,
				Title:  github.Ptr("PROBER TEST"),
				State:  github.Ptr("open"),
			}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			return &github.Issue{Number: &number, State: github.Ptr("closed")}, nil
		},
	}
	mockReg := &mockRegistrationClient{}

	cfg := &Config{
		Repo:          "brotherlogic/ghwebhook",
		GHWebhookAddr: "localhost:50051",
		ListenAddr:    "127.0.0.1:0",
		ServiceAddr:   "127.0.0.1:0",
		Timeout:       50 * time.Millisecond,
	}

	p := prober.NewProber(
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
		prober.WithHookClient(defaultTestHookClient()),
	)

	var stdout, stderr bytes.Buffer
	exitCode := run(context.Background(), cfg, p, mockGH, cfg.Repo, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected exit code 1 when alert creation fails, got %d", exitCode)
	}
}

func TestParseConfig_MetricsDefaults(t *testing.T) {
	getenv := func(key string) string {
		if key == "GH_TOKEN" {
			return "default-token"
		}
		return ""
	}
	cfg, err := parseConfig([]string{}, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.MetricsAddr != ":8081" {
		t.Errorf("expected MetricsAddr %q, got %q", ":8081", cfg.MetricsAddr)
	}
	if cfg.MetricsHoldTimeout != 30*time.Second {
		t.Errorf("expected MetricsHoldTimeout 30s, got %v", cfg.MetricsHoldTimeout)
	}
}

func TestParseConfig_MetricsEnvVars(t *testing.T) {
	env := map[string]string{
		"GH_TOKEN":                    "default-token",
		"PROBER_METRICS_ADDR":         ":9090",
		"PROBER_METRICS_HOLD_TIMEOUT": "15s",
	}
	getenv := func(key string) string { return env[key] }

	cfg, err := parseConfig([]string{}, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.MetricsAddr != ":9090" {
		t.Errorf("expected MetricsAddr %q, got %q", ":9090", cfg.MetricsAddr)
	}
	if cfg.MetricsHoldTimeout != 15*time.Second {
		t.Errorf("expected MetricsHoldTimeout 15s, got %v", cfg.MetricsHoldTimeout)
	}
}

func TestParseConfig_MetricsFlagOverrides(t *testing.T) {
	env := map[string]string{
		"GH_TOKEN":                    "default-token",
		"PROBER_METRICS_ADDR":         ":9090",
		"PROBER_METRICS_HOLD_TIMEOUT": "15s",
	}
	getenv := func(key string) string { return env[key] }

	args := []string{
		"--metrics-addr=:9091",
		"--metrics-hold-timeout=45s",
	}

	cfg, err := parseConfig(args, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.MetricsAddr != ":9091" {
		t.Errorf("expected MetricsAddr %q, got %q", ":9091", cfg.MetricsAddr)
	}
	if cfg.MetricsHoldTimeout != 45*time.Second {
		t.Errorf("expected MetricsHoldTimeout 45s, got %v", cfg.MetricsHoldTimeout)
	}
}

func TestParseConfig_InvalidMetricsHoldTimeout(t *testing.T) {
	env := map[string]string{
		"GH_TOKEN":                    "default-token",
		"PROBER_METRICS_HOLD_TIMEOUT": "invalid-duration",
	}
	getenv := func(key string) string { return env[key] }

	_, err := parseConfig([]string{}, getenv)
	if err == nil {
		t.Fatalf("expected error on invalid PROBER_METRICS_HOLD_TIMEOUT, got nil")
	}
}

func TestRun_Pipeline_InvokesRecordResultAndServeMetrics_Success(t *testing.T) {
	createdNumber := 301
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
		Repo:               "brotherlogic/ghwebhook",
		GHWebhookAddr:      "localhost:50051",
		ListenAddr:         "127.0.0.1:0",
		ServiceAddr:        "127.0.0.1:0",
		Timeout:            2 * time.Second,
		MetricsAddr:        ":8089",
		MetricsHoldTimeout: 10 * time.Second,
	}

	p := prober.NewProber(
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
		prober.WithHookClient(defaultTestHookClient()),
	)

	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = p.ReceiveWebhook(context.Background(), &pb.WebhookEvent{
			Header: &pb.EventHeader{EventType: "issues"},
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

	var recordedRepo string
	var recordedResult prober.Result
	var recordCalled bool

	var servedAddr string
	var servedTimeout time.Duration
	var serveCalled bool

	origRecord := recordResultFunc
	origServe := serveMetricsUntilScrapedFunc
	defer func() {
		recordResultFunc = origRecord
		serveMetricsUntilScrapedFunc = origServe
	}()

	recordResultFunc = func(repo string, res prober.Result) {
		recordCalled = true
		recordedRepo = repo
		recordedResult = res
	}
	serveMetricsUntilScrapedFunc = func(ctx context.Context, addr string, timeout time.Duration) error {
		serveCalled = true
		servedAddr = addr
		servedTimeout = timeout
		return nil
	}

	var stdout, stderr bytes.Buffer
	exitCode := run(context.Background(), cfg, p, mockGH, cfg.Repo, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 on success, got %d", exitCode)
	}

	if !recordCalled {
		t.Fatalf("expected RecordResult to be invoked, but was not")
	}
	if recordedRepo != cfg.Repo {
		t.Errorf("expected recorded repo %q, got %q", cfg.Repo, recordedRepo)
	}
	if recordedResult.Status != prober.StatusSuccess {
		t.Errorf("expected recorded status %v, got %v", prober.StatusSuccess, recordedResult.Status)
	}

	if !serveCalled {
		t.Fatalf("expected ServeMetricsUntilScraped to be invoked, but was not")
	}
	if servedAddr != ":8089" {
		t.Errorf("expected served addr %q, got %q", ":8089", servedAddr)
	}
	if servedTimeout != 10*time.Second {
		t.Errorf("expected served timeout 10s, got %v", servedTimeout)
	}
}

func TestRun_Pipeline_InvokesRecordResultAndServeMetrics_SoftFailure(t *testing.T) {
	mockGH := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return nil, errors.New("rate limit exceeded (403)")
		},
	}
	mockReg := &mockRegistrationClient{}

	cfg := &Config{
		Repo:               "brotherlogic/ghwebhook",
		GHWebhookAddr:      "localhost:50051",
		ListenAddr:         "127.0.0.1:0",
		ServiceAddr:        "127.0.0.1:0",
		Timeout:            1 * time.Second,
		MetricsAddr:        ":8088",
		MetricsHoldTimeout: 5 * time.Second,
	}

	p := prober.NewProber(
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
		prober.WithHookClient(defaultTestHookClient()),
	)

	var recordedResult prober.Result
	var recordCalled bool
	var serveCalled bool

	origRecord := recordResultFunc
	origServe := serveMetricsUntilScrapedFunc
	defer func() {
		recordResultFunc = origRecord
		serveMetricsUntilScrapedFunc = origServe
	}()

	recordResultFunc = func(repo string, res prober.Result) {
		recordCalled = true
		recordedResult = res
	}
	serveMetricsUntilScrapedFunc = func(ctx context.Context, addr string, timeout time.Duration) error {
		serveCalled = true
		return nil
	}

	var stdout, stderr bytes.Buffer
	exitCode := run(context.Background(), cfg, p, mockGH, cfg.Repo, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 on soft failure, got %d", exitCode)
	}

	if !recordCalled {
		t.Fatalf("expected RecordResult to be called on soft failure")
	}
	if recordedResult.Status != prober.StatusSoftFailure {
		t.Errorf("expected StatusSoftFailure, got %v", recordedResult.Status)
	}
	if !serveCalled {
		t.Fatalf("expected ServeMetricsUntilScraped to be called on soft failure")
	}
}

func TestRun_Pipeline_InvokesRecordResultAndServeMetrics_HardFailure(t *testing.T) {
	alertIssueNum := 888
	alertURL := "https://github.com/brotherlogic/ghwebhook/issues/888"
	createdNumber := 302
	mockGH := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			if req.GetTitle() == prober.DefaultAlertTitle {
				return &github.Issue{
					Number:  &alertIssueNum,
					HTMLURL: &alertURL,
					Title:   github.Ptr(prober.DefaultAlertTitle),
					State:   github.Ptr("open"),
				}, nil
			}
			return &github.Issue{
				Number: &createdNumber,
				Title:  github.Ptr("PROBER TEST"),
				State:  github.Ptr("open"),
			}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			return &github.Issue{Number: &number, State: github.Ptr("closed")}, nil
		},
	}
	mockReg := &mockRegistrationClient{}

	cfg := &Config{
		Repo:               "brotherlogic/ghwebhook",
		GHWebhookAddr:      "localhost:50051",
		ListenAddr:         "127.0.0.1:0",
		ServiceAddr:        "127.0.0.1:0",
		Timeout:            50 * time.Millisecond,
		MetricsAddr:        ":8087",
		MetricsHoldTimeout: 5 * time.Second,
	}

	p := prober.NewProber(
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
	)

	var recordedResult prober.Result
	var recordCalled bool
	var serveCalled bool

	origRecord := recordResultFunc
	origServe := serveMetricsUntilScrapedFunc
	defer func() {
		recordResultFunc = origRecord
		serveMetricsUntilScrapedFunc = origServe
	}()

	recordResultFunc = func(repo string, res prober.Result) {
		recordCalled = true
		recordedResult = res
	}
	serveMetricsUntilScrapedFunc = func(ctx context.Context, addr string, timeout time.Duration) error {
		serveCalled = true
		return nil
	}

	var stdout, stderr bytes.Buffer
	exitCode := run(context.Background(), cfg, p, mockGH, cfg.Repo, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 on hard failure with successful alert, got %d", exitCode)
	}

	if !recordCalled {
		t.Fatalf("expected RecordResult to be called on hard failure")
	}
	if recordedResult.Status != prober.StatusHardFailure {
		t.Errorf("expected StatusHardFailure, got %v", recordedResult.Status)
	}
	if !serveCalled {
		t.Fatalf("expected ServeMetricsUntilScraped to be called on hard failure")
	}
}

func TestRun_MetricsScrapeServerError_DoesNotAlterExitCode_Success(t *testing.T) {
	createdNumber := 303
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
		Repo:               "brotherlogic/ghwebhook",
		GHWebhookAddr:      "localhost:50051",
		ListenAddr:         "127.0.0.1:0",
		ServiceAddr:        "127.0.0.1:0",
		Timeout:            2 * time.Second,
		MetricsAddr:        ":8081",
		MetricsHoldTimeout: 30 * time.Second,
	}

	p := prober.NewProber(
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
		prober.WithHookClient(defaultTestHookClient()),
	)

	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = p.ReceiveWebhook(context.Background(), &pb.WebhookEvent{
			Header: &pb.EventHeader{EventType: "issues"},
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

	origServe := serveMetricsUntilScrapedFunc
	defer func() {
		serveMetricsUntilScrapedFunc = origServe
	}()

	serveMetricsUntilScrapedFunc = func(ctx context.Context, addr string, timeout time.Duration) error {
		return errors.New("listen tcp :8081: bind: address already in use")
	}

	var stdout, stderr bytes.Buffer
	exitCode := run(context.Background(), cfg, p, mockGH, cfg.Repo, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 despite scrape server error, got %d", exitCode)
	}
}

func TestRun_MetricsScrapeServerError_DoesNotAlterExitCode_HardFailureAlertFailure(t *testing.T) {
	createdNumber := 304
	mockGH := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			if strings.Contains(query, prober.DefaultAlertTitle) {
				return nil, errors.New("github API outage during alert search")
			}
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			return &github.Issue{
				Number: &createdNumber,
				Title:  github.Ptr("PROBER TEST"),
				State:  github.Ptr("open"),
			}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			return &github.Issue{Number: &number, State: github.Ptr("closed")}, nil
		},
	}
	mockReg := &mockRegistrationClient{}

	cfg := &Config{
		Repo:               "brotherlogic/ghwebhook",
		GHWebhookAddr:      "localhost:50051",
		ListenAddr:         "127.0.0.1:0",
		ServiceAddr:        "127.0.0.1:0",
		Timeout:            50 * time.Millisecond,
		MetricsAddr:        ":8081",
		MetricsHoldTimeout: 30 * time.Second,
	}

	p := prober.NewProber(
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
		prober.WithHookClient(defaultTestHookClient()),
	)

	origServe := serveMetricsUntilScrapedFunc
	defer func() {
		serveMetricsUntilScrapedFunc = origServe
	}()

	serveMetricsUntilScrapedFunc = func(ctx context.Context, addr string, timeout time.Duration) error {
		return errors.New("listen tcp :8081: bind: address already in use")
	}

	var stdout, stderr bytes.Buffer
	exitCode := run(context.Background(), cfg, p, mockGH, cfg.Repo, &stdout, &stderr)
	// On hard failure when alert creation fails, exit code must remain 1.
	if exitCode != 1 {
		t.Fatalf("expected exit code 1 when alert fails on hard failure, got %d", exitCode)
	}
}

func TestRun_RealServeMetricsUntilScraped_Integration(t *testing.T) {
	origServe := serveMetricsUntilScrapedFunc
	origRecord := recordResultFunc
	defer func() {
		serveMetricsUntilScrapedFunc = origServe
		recordResultFunc = origRecord
	}()

	serveMetricsUntilScrapedFunc = prober.ServeMetricsUntilScraped
	recordResultFunc = prober.RecordResult

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on free port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	createdNumber := 305
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
		Repo:               "brotherlogic/ghwebhook",
		GHWebhookAddr:      "localhost:50051",
		ListenAddr:         "127.0.0.1:0",
		ServiceAddr:        "127.0.0.1:0",
		Timeout:            2 * time.Second,
		MetricsAddr:        addr,
		MetricsHoldTimeout: 5 * time.Second,
	}

	p := prober.NewProber(
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
		prober.WithHookClient(defaultTestHookClient()),
	)

	scraped := make(chan bool, 1)
	go func() {
		client := &http.Client{Timeout: 500 * time.Millisecond}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			resp, getErr := client.Get("http://" + addr + "/metrics")
			if getErr == nil {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK && strings.Contains(string(body), "ghwebhook_prober_runs_total") {
					scraped <- true
					return
				}
			}
		}
		scraped <- false
	}()

	var stdout, stderr bytes.Buffer
	exitCode := run(context.Background(), cfg, p, mockGH, cfg.Repo, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	select {
	case ok := <-scraped:
		if !ok {
			t.Errorf("failed to scrape metrics endpoint or missing expected metric")
		}
	case <-time.After(2 * time.Second):
		t.Errorf("timed out waiting for scraper goroutine")
	}
}

func TestRunWithProber_WiresGitHubHookClient(t *testing.T) {
	cfg := &Config{
		Repo:          "brotherlogic/ghwebhook",
		GHWebhookAddr: "localhost:50051",
		ListenAddr:    "127.0.0.1:0",
		ServiceAddr:   "127.0.0.1:0",
		Timeout:       100 * time.Millisecond,
		GitHubToken:   "test-token-for-hook-wiring",
	}

	var capturedHookClient prober.GitHubHookClient
	captureOpt := func(p *prober.Prober) {
		capturedHookClient = p.HookClient()
	}

	mockGH := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return nil, errors.New("transient error")
		},
	}
	mockReg := &mockRegistrationClient{}

	var stdout, stderr bytes.Buffer
	_ = runWithProber(context.Background(), cfg, &stdout, &stderr,
		captureOpt,
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
	)

	if capturedHookClient == nil {
		t.Fatalf("expected prober to have a non-nil GitHubHookClient wired by runWithProber, got nil")
	}
}

func TestRunWithProber_ExtraOptsOverrideHookClient(t *testing.T) {
	cfg := &Config{
		Repo:          "brotherlogic/ghwebhook",
		GHWebhookAddr: "localhost:50051",
		ListenAddr:    "127.0.0.1:0",
		ServiceAddr:   "127.0.0.1:0",
		Timeout:       100 * time.Millisecond,
		GitHubToken:   "test-token-for-hook-wiring",
	}

	explicitMockHook := &prober.MockGitHubHookClient{}
	var capturedHookClient prober.GitHubHookClient
	captureOpt := func(p *prober.Prober) {
		capturedHookClient = p.HookClient()
	}

	mockGH := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return nil, errors.New("transient error")
		},
	}
	mockReg := &mockRegistrationClient{}

	var stdout, stderr bytes.Buffer
	_ = runWithProber(context.Background(), cfg, &stdout, &stderr,
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
		prober.WithHookClient(explicitMockHook),
		captureOpt,
	)

	if capturedHookClient != explicitMockHook {
		t.Fatalf("expected explicit WithHookClient from extraOpts to take precedence, got %v", capturedHookClient)
	}
}

func TestParseConfig_IngressURLEnvVar(t *testing.T) {
	env := map[string]string{
		"GH_TOKEN":           "test-token",
		"PROBER_INGRESS_URL": "https://env.example.com/ingress",
	}
	getenv := func(key string) string { return env[key] }

	cfg, err := parseConfig([]string{}, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.IngressURL != "https://env.example.com/ingress" {
		t.Errorf("expected IngressURL %q, got %q", "https://env.example.com/ingress", cfg.IngressURL)
	}
}

func TestParseConfig_IngressURLFlagOverrides(t *testing.T) {
	env := map[string]string{
		"GH_TOKEN":           "test-token",
		"PROBER_INGRESS_URL": "https://env.example.com/ingress",
	}
	getenv := func(key string) string { return env[key] }

	args := []string{
		"--ingress-url=https://flag.example.com/ingress",
	}

	cfg, err := parseConfig(args, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.IngressURL != "https://flag.example.com/ingress" {
		t.Errorf("expected IngressURL %q, got %q", "https://flag.example.com/ingress", cfg.IngressURL)
	}
}

func TestRunWithProber_WiresIngressURL(t *testing.T) {
	cfg := &Config{
		Repo:          "brotherlogic/ghwebhook",
		GHWebhookAddr: "localhost:50051",
		ListenAddr:    "127.0.0.1:0",
		ServiceAddr:   "127.0.0.1:0",
		Timeout:       100 * time.Millisecond,
		GitHubToken:   "test-token",
		IngressURL:    "https://prober.example.com/ingress",
	}

	var capturedIngressURL string
	captureOpt := func(p *prober.Prober) {
		capturedIngressURL = p.IngressURL()
	}

	mockGH := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return nil, errors.New("transient error")
		},
	}
	mockReg := &mockRegistrationClient{}

	var stdout, stderr bytes.Buffer
	_ = runWithProber(context.Background(), cfg, &stdout, &stderr,
		prober.WithGitHubClient(mockGH),
		prober.WithRegistrationClient(mockReg),
		captureOpt,
	)

	if capturedIngressURL != "https://prober.example.com/ingress" {
		t.Fatalf("expected captured IngressURL %q, got %q", "https://prober.example.com/ingress", capturedIngressURL)
	}
}
