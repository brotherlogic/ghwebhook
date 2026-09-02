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
	if owner != "" && repo != "" && !strings.Contains(query, "repo:") {
		q = fmt.Sprintf("repo:%s/%s %s", owner, repo, query)
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
