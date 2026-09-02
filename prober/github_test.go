package prober_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brotherlogic/ghwebhook/prober"
	"github.com/google/go-github/v69/github"
)

func TestGitHubIssueClient_Mock(t *testing.T) {
	issueNum := 42
	stateOpen := "open"
	stateClosed := "closed"
	title := "PROBER TEST"

	mockClient := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			if owner != "brotherlogic" || repo != "ghwebhook" {
				t.Errorf("unexpected repo: %s/%s", owner, repo)
			}
			return []*github.Issue{
				{
					Number: &issueNum,
					Title:  &title,
					State:  &stateOpen,
				},
			}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			if req.GetTitle() != title {
				t.Errorf("unexpected title: %s", req.GetTitle())
			}
			return &github.Issue{
				Number: &issueNum,
				Title:  req.Title,
				State:  &stateOpen,
			}, nil
		},
		EditIssueFunc: func(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error) {
			if number != 42 {
				t.Errorf("unexpected issue number: %d", number)
			}
			return &github.Issue{
				Number: &number,
				State:  req.State,
			}, nil
		},
	}

	var client prober.GitHubIssueClient = mockClient

	// 1. Verify Search
	issues, err := client.SearchIssues(context.Background(), "brotherlogic", "ghwebhook", `is:issue "PROBER TEST"`)
	if err != nil {
		t.Fatalf("SearchIssues failed: %v", err)
	}
	if len(issues) != 1 || issues[0].GetNumber() != 42 {
		t.Fatalf("unexpected search result: %+v", issues)
	}

	// 2. Verify Create
	createReq := &github.IssueRequest{
		Title: github.Ptr(title),
		Body:  github.Ptr("automated test"),
	}
	created, err := client.CreateIssue(context.Background(), "brotherlogic", "ghwebhook", createReq)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if created.GetNumber() != 42 || created.GetState() != "open" {
		t.Fatalf("unexpected created issue: %+v", created)
	}

	// 3. Verify Edit (Close)
	closeReq := &github.IssueRequest{
		State: github.Ptr(stateClosed),
	}
	closed, err := client.EditIssue(context.Background(), "brotherlogic", "ghwebhook", 42, closeReq)
	if err != nil {
		t.Fatalf("EditIssue (close) failed: %v", err)
	}
	if closed.GetState() != "closed" {
		t.Fatalf("unexpected state after close: %s", closed.GetState())
	}

	// 4. Verify Edit (Reopen)
	reopenReq := &github.IssueRequest{
		State: github.Ptr(stateOpen),
	}
	reopened, err := client.EditIssue(context.Background(), "brotherlogic", "ghwebhook", 42, reopenReq)
	if err != nil {
		t.Fatalf("EditIssue (reopen) failed: %v", err)
	}
	if reopened.GetState() != "open" {
		t.Fatalf("unexpected state after reopen: %s", reopened.GetState())
	}
}

func TestNewDefaultGitHubIssueClient_TokenResolution(t *testing.T) {
	// 1. Explicit token
	c1 := prober.NewDefaultGitHubIssueClient("explicit-token")
	if c1 == nil {
		t.Fatal("expected non-nil client with explicit token")
	}

	// 2. Fallback to GH_TOKEN
	t.Setenv("GH_TOKEN", "gh-env-token")
	t.Setenv("GITHUB_TOKEN", "")
	c2 := prober.NewDefaultGitHubIssueClient("")
	if c2 == nil {
		t.Fatal("expected non-nil client with GH_TOKEN")
	}

	// 3. Fallback to GITHUB_TOKEN
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "github-env-token")
	c3 := prober.NewDefaultGitHubIssueClient("")
	if c3 == nil {
		t.Fatal("expected non-nil client with GITHUB_TOKEN")
	}

	// 4. Unauthenticated fallback
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	c4 := prober.NewDefaultGitHubIssueClient("")
	if c4 == nil {
		t.Fatal("expected non-nil client with empty token")
	}
}

func TestNewGitHubIssueClientFromClient(t *testing.T) {
	rawClient := github.NewClient(nil)
	c := prober.NewGitHubIssueClientFromClient(rawClient)
	if c == nil {
		t.Fatal("expected non-nil client from existing *github.Client")
	}
}

func TestDefaultGitHubIssueClient_LiveHttpEndpoints(t *testing.T) {
	issueNum := 101
	title := "PROBER TEST"
	stateOpen := "open"
	stateClosed := "closed"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/search/issues"):
			q := r.URL.Query().Get("q")
			if !strings.Contains(q, "repo:brotherlogic/ghwebhook") {
				t.Errorf("expected search query to scope repo, got q=%q", q)
			}
			result := github.IssuesSearchResult{
				Total: github.Ptr(1),
				Issues: []*github.Issue{
					{
						Number: github.Ptr(issueNum),
						Title:  github.Ptr(title),
						State:  github.Ptr(stateOpen),
					},
				},
			}
			_ = json.NewEncoder(w).Encode(result)

		case r.Method == http.MethodPost && r.URL.Path == "/repos/brotherlogic/ghwebhook/issues":
			var req github.IssueRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			resp := github.Issue{
				Number: github.Ptr(issueNum),
				Title:  req.Title,
				State:  github.Ptr(stateOpen),
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == http.MethodPatch && r.URL.Path == "/repos/brotherlogic/ghwebhook/issues/101":
			var req github.IssueRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			resp := github.Issue{
				Number: github.Ptr(issueNum),
				Title:  github.Ptr(title),
				State:  req.State,
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)

		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	gh := github.NewClient(ts.Client())
	gh.BaseURL, _ = gh.BaseURL.Parse(ts.URL + "/")

	client := prober.NewGitHubIssueClientFromClient(gh)
	ctx := context.Background()

	// 1. Search Issues
	issues, err := client.SearchIssues(ctx, "brotherlogic", "ghwebhook", `"PROBER TEST"`)
	if err != nil {
		t.Fatalf("SearchIssues failed: %v", err)
	}
	if len(issues) != 1 || issues[0].GetNumber() != 101 {
		t.Fatalf("unexpected issues: %+v", issues)
	}

	// 2. Create Issue
	created, err := client.CreateIssue(ctx, "brotherlogic", "ghwebhook", &github.IssueRequest{
		Title: github.Ptr("PROBER TEST"),
	})
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if created.GetNumber() != 101 || created.GetState() != "open" {
		t.Fatalf("unexpected created issue: %+v", created)
	}

	// 3. Edit Issue (close)
	closed, err := client.EditIssue(ctx, "brotherlogic", "ghwebhook", 101, &github.IssueRequest{
		State: github.Ptr(stateClosed),
	})
	if err != nil {
		t.Fatalf("EditIssue (close) failed: %v", err)
	}
	if closed.GetState() != "closed" {
		t.Fatalf("unexpected closed issue: %+v", closed)
	}
}
