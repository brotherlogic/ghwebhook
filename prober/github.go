package prober

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-github/v69/github"
)

// GitHubIssueClient defines the interface for interacting with GitHub issues.
type GitHubIssueClient interface {
	SearchIssues(ctx context.Context, owner, repo, query string) ([]*github.Issue, error)
	CreateIssue(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error)
	EditIssue(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error)
}

type defaultGitHubIssueClient struct {
	client *github.Client
}

// NewDefaultGitHubIssueClient creates a GitHubIssueClient authenticated with the given token.
// If token is empty, it attempts to resolve the token from GH_TOKEN or GITHUB_TOKEN environment variables.
func NewDefaultGitHubIssueClient(token string) GitHubIssueClient {
	if token == "" {
		token = os.Getenv("GH_TOKEN")
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}
	}

	var client *github.Client
	if token != "" {
		client = github.NewClient(nil).WithAuthToken(token)
	} else {
		client = github.NewClient(nil)
	}

	return &defaultGitHubIssueClient{client: client}
}

// NewGitHubIssueClientFromClient creates a GitHubIssueClient wrapping an existing *github.Client.
func NewGitHubIssueClientFromClient(client *github.Client) GitHubIssueClient {
	return &defaultGitHubIssueClient{client: client}
}

func (d *defaultGitHubIssueClient) SearchIssues(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
	q := query
	if !strings.Contains(q, "is:issue") && !strings.Contains(q, "is:pull-request") {
		q = "is:issue " + q
	}
	if owner != "" && repo != "" && !strings.Contains(q, "repo:") {
		q = fmt.Sprintf("repo:%s/%s %s", owner, repo, q)
	}
	result, _, err := d.client.Search.Issues(ctx, q, nil)
	if err != nil {
		return nil, err
	}
	return result.Issues, nil
}

func (d *defaultGitHubIssueClient) CreateIssue(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
	issue, _, err := d.client.Issues.Create(ctx, owner, repo, req)
	return issue, err
}

func (d *defaultGitHubIssueClient) EditIssue(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
	issue, _, err := d.client.Issues.Edit(ctx, owner, repo, number, req)
	return issue, err
}

// MockGitHubIssueClient is a configurable mock implementation of GitHubIssueClient for unit testing.
type MockGitHubIssueClient struct {
	SearchIssuesFunc func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error)
	CreateIssueFunc  func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error)
	EditIssueFunc    func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error)
}

func (m *MockGitHubIssueClient) SearchIssues(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
	if m.SearchIssuesFunc != nil {
		return m.SearchIssuesFunc(ctx, owner, repo, query)
	}
	return nil, nil
}

func (m *MockGitHubIssueClient) CreateIssue(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
	if m.CreateIssueFunc != nil {
		return m.CreateIssueFunc(ctx, owner, repo, req)
	}
	return nil, nil
}

func (m *MockGitHubIssueClient) EditIssue(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
	if m.EditIssueFunc != nil {
		return m.EditIssueFunc(ctx, owner, repo, number, req)
	}
	return nil, nil
}

// GitHubHookClient defines the interface for interacting with GitHub repository webhooks and deliveries.
type GitHubHookClient interface {
	ListHooks(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error)
	ListHookDeliveries(ctx context.Context, owner, repo string, hookID int64, opts *github.ListCursorOptions) ([]*github.HookDelivery, error)
}

type defaultGitHubHookClient struct {
	client *github.Client
}

// NewDefaultGitHubHookClient creates a GitHubHookClient authenticated with the given token.
// If token is empty, it attempts to resolve the token from GH_TOKEN or GITHUB_TOKEN environment variables.
func NewDefaultGitHubHookClient(token string) GitHubHookClient {
	if token == "" {
		token = os.Getenv("GH_TOKEN")
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}
	}

	var client *github.Client
	if token != "" {
		client = github.NewClient(nil).WithAuthToken(token)
	} else {
		client = github.NewClient(nil)
	}

	return &defaultGitHubHookClient{client: client}
}

// NewGitHubHookClientFromClient creates a GitHubHookClient wrapping an existing *github.Client.
func NewGitHubHookClientFromClient(client *github.Client) GitHubHookClient {
	return &defaultGitHubHookClient{client: client}
}

func (d *defaultGitHubHookClient) ListHooks(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
	hooks, _, err := d.client.Repositories.ListHooks(ctx, owner, repo, opts)
	return hooks, err
}

func (d *defaultGitHubHookClient) ListHookDeliveries(ctx context.Context, owner, repo string, hookID int64, opts *github.ListCursorOptions) ([]*github.HookDelivery, error) {
	deliveries, _, err := d.client.Repositories.ListHookDeliveries(ctx, owner, repo, hookID, opts)
	return deliveries, err
}

// MockGitHubHookClient is a configurable mock implementation of GitHubHookClient for unit testing.
type MockGitHubHookClient struct {
	ListHooksFunc          func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error)
	ListHookDeliveriesFunc func(ctx context.Context, owner, repo string, hookID int64, opts *github.ListCursorOptions) ([]*github.HookDelivery, error)
}

func (m *MockGitHubHookClient) ListHooks(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
	if m.ListHooksFunc != nil {
		return m.ListHooksFunc(ctx, owner, repo, opts)
	}
	return nil, nil
}

func (m *MockGitHubHookClient) ListHookDeliveries(ctx context.Context, owner, repo string, hookID int64, opts *github.ListCursorOptions) ([]*github.HookDelivery, error) {
	if m.ListHookDeliveriesFunc != nil {
		return m.ListHookDeliveriesFunc(ctx, owner, repo, hookID, opts)
	}
	return nil, nil
}

