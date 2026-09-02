package prober

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"github.com/google/go-github/v69/github"
	"google.golang.org/grpc"
)

type mockGitHubIssueClient struct {
	searchIssuesFunc func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error)
	createIssueFunc  func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error)
	editIssueFunc    func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error)
}

func (m *mockGitHubIssueClient) SearchIssues(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
	if m.searchIssuesFunc != nil {
		return m.searchIssuesFunc(ctx, owner, repo, query)
	}
	return nil, nil
}

func (m *mockGitHubIssueClient) CreateIssue(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
	if m.createIssueFunc != nil {
		return m.createIssueFunc(ctx, owner, repo, req)
	}
	return nil, nil
}

func (m *mockGitHubIssueClient) EditIssue(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
	if m.editIssueFunc != nil {
		return m.editIssueFunc(ctx, owner, repo, number, req)
	}
	return nil, nil
}

type mockRegistrationClient struct {
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

func TestResultStatus_EnumAndString(t *testing.T) {
	tests := []struct {
		status   ResultStatus
		expected int
		strVal   string
	}{
		{StatusSuccess, 0, "SUCCESS"},
		{StatusHardFailure, 1, "HARD_FAILURE"},
		{StatusSoftFailure, 2, "SOFT_FAILURE"},
		{ResultStatus(999), 999, "UNKNOWN"},
	}

	for _, tt := range tests {
		if int(tt.status) != tt.expected {
			t.Errorf("ResultStatus enum value for %s got %d, want %d", tt.strVal, int(tt.status), tt.expected)
		}
		if tt.status.String() != tt.strVal {
			t.Errorf("ResultStatus.String() got %q, want %q", tt.status.String(), tt.strVal)
		}
	}
}

func TestResult_StructFields(t *testing.T) {
	testErr := errors.New("sample error")
	res := Result{
		Status:      StatusSuccess,
		Duration:    1500 * time.Millisecond,
		IssueNumber: 42,
		Action:      "opened",
		Message:     "webhook received successfully",
		Err:         testErr,
	}

	if res.Status != StatusSuccess {
		t.Errorf("res.Status = %v, want %v", res.Status, StatusSuccess)
	}
	if res.Duration != 1500*time.Millisecond {
		t.Errorf("res.Duration = %v, want %v", res.Duration, 1500*time.Millisecond)
	}
	if res.IssueNumber != 42 {
		t.Errorf("res.IssueNumber = %d, want 42", res.IssueNumber)
	}
	if res.Action != "opened" {
		t.Errorf("res.Action = %q, want 'opened'", res.Action)
	}
	if res.Message != "webhook received successfully" {
		t.Errorf("res.Message = %q, want 'webhook received successfully'", res.Message)
	}
	if !errors.Is(res.Err, testErr) {
		t.Errorf("res.Err = %v, want %v", res.Err, testErr)
	}
}

func TestNewProber_Defaults(t *testing.T) {
	p := NewProber()

	if p == nil {
		t.Fatal("NewProber() returned nil")
	}
	if p.repoFullName != DefaultRepo {
		t.Errorf("p.repoFullName = %q, want %q", p.repoFullName, DefaultRepo)
	}
	if p.targetTitle != DefaultTargetTitle {
		t.Errorf("p.targetTitle = %q, want %q", p.targetTitle, DefaultTargetTitle)
	}
	if p.ghwebhookAddr != DefaultGHWebhookAddr {
		t.Errorf("p.ghwebhookAddr = %q, want %q", p.ghwebhookAddr, DefaultGHWebhookAddr)
	}
	if p.listenAddr != DefaultListenAddr {
		t.Errorf("p.listenAddr = %q, want %q", p.listenAddr, DefaultListenAddr)
	}
	if p.serviceAddr != DefaultServiceAddr {
		t.Errorf("p.serviceAddr = %q, want %q", p.serviceAddr, DefaultServiceAddr)
	}
	if p.timeout != DefaultTimeout {
		t.Errorf("p.timeout = %v, want %v", p.timeout, DefaultTimeout)
	}
	if p.eventCh == nil {
		t.Error("p.eventCh is nil, expected initialized channel")
	}
	if p.ghClient != nil {
		t.Errorf("p.ghClient = %v, want nil by default", p.ghClient)
	}
	if p.regClient != nil {
		t.Errorf("p.regClient = %v, want nil by default", p.regClient)
	}
}

func TestNewProber_Options(t *testing.T) {
	mockGH := &mockGitHubIssueClient{}
	mockReg := &mockRegistrationClient{}

	p := NewProber(
		WithRepo("octocat/Hello-World"),
		WithTargetTitle("CUSTOM PROBER TEST"),
		WithTargetIssueNumber(123),
		WithTargetAction("opened"),
		WithGHWebhookAddr("10.0.0.1:50051"),
		WithListenAddr(":60052"),
		WithServiceAddr("prober.test.svc:60052"),
		WithTimeout(30*time.Second),
		WithGitHubClient(mockGH),
		WithRegistrationClient(mockReg),
	)

	if p.repoFullName != "octocat/Hello-World" {
		t.Errorf("p.repoFullName = %q, want 'octocat/Hello-World'", p.repoFullName)
	}
	if p.targetTitle != "CUSTOM PROBER TEST" {
		t.Errorf("p.targetTitle = %q, want 'CUSTOM PROBER TEST'", p.targetTitle)
	}
	if p.targetIssueNumber != 123 {
		t.Errorf("p.targetIssueNumber = %d, want 123", p.targetIssueNumber)
	}
	if p.targetAction != "opened" {
		t.Errorf("p.targetAction = %q, want 'opened'", p.targetAction)
	}
	if p.ghwebhookAddr != "10.0.0.1:50051" {
		t.Errorf("p.ghwebhookAddr = %q, want '10.0.0.1:50051'", p.ghwebhookAddr)
	}
	if p.ListenAddr() != ":60052" {
		t.Errorf("p.ListenAddr() = %q, want ':60052'", p.ListenAddr())
	}
	if p.serviceAddr != "prober.test.svc:60052" {
		t.Errorf("p.serviceAddr = %q, want 'prober.test.svc:60052'", p.serviceAddr)
	}
	if p.timeout != 30*time.Second {
		t.Errorf("p.timeout = %v, want 30s", p.timeout)
	}
	if p.ghClient != mockGH {
		t.Errorf("p.ghClient = %v, want %v", p.ghClient, mockGH)
	}
	if p.regClient != mockReg {
		t.Errorf("p.regClient = %v, want %v", p.regClient, mockReg)
	}
	if p.EventChannel() == nil {
		t.Error("p.EventChannel() is nil, expected valid channel")
	}
}

func TestProber_Setters(t *testing.T) {
	p := NewProber()
	p.SetTargetIssueNumber(456)
	p.SetTargetAction("reopened")

	if p.targetIssueNumber != 456 {
		t.Errorf("p.targetIssueNumber = %d, want 456", p.targetIssueNumber)
	}
	if p.targetAction != "reopened" {
		t.Errorf("p.targetAction = %q, want 'reopened'", p.targetAction)
	}
}
