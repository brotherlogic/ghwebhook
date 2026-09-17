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
			if !strings.Contains(q, "is:issue") {
				t.Errorf("expected search query to specify is:issue, got q=%q", q)
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

func TestDefaultGitHubIssueClient_SearchQueryFormatting(t *testing.T) {
	var capturedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		capturedQuery = r.URL.Query().Get("q")
		_ = json.NewEncoder(w).Encode(github.IssuesSearchResult{})
	}))
	defer ts.Close()

	gh := github.NewClient(ts.Client())
	gh.BaseURL, _ = gh.BaseURL.Parse(ts.URL + "/")
	client := prober.NewGitHubIssueClientFromClient(gh)
	ctx := context.Background()

	tests := []struct {
		name       string
		owner      string
		repo       string
		query      string
		wantSubstr []string
	}{
		{
			name:       "bare query adds repo and is:issue",
			owner:      "brotherlogic",
			repo:       "ghwebhook",
			query:      "PROBER TEST",
			wantSubstr: []string{"repo:brotherlogic/ghwebhook", "is:issue", "PROBER TEST"},
		},
		{
			name:       "query already containing is:issue does not duplicate",
			owner:      "brotherlogic",
			repo:       "ghwebhook",
			query:      "is:issue PROBER TEST",
			wantSubstr: []string{"repo:brotherlogic/ghwebhook", "is:issue", "PROBER TEST"},
		},
		{
			name:       "query already containing is:pull-request does not add is:issue",
			owner:      "brotherlogic",
			repo:       "ghwebhook",
			query:      "is:pull-request PROBER TEST",
			wantSubstr: []string{"repo:brotherlogic/ghwebhook", "is:pull-request"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capturedQuery = ""
			_, err := client.SearchIssues(ctx, tt.owner, tt.repo, tt.query)
			if err != nil {
				t.Fatalf("SearchIssues error: %v", err)
			}
			for _, substr := range tt.wantSubstr {
				if !strings.Contains(capturedQuery, substr) {
					t.Errorf("capturedQuery = %q, want substring %q", capturedQuery, substr)
				}
			}
			if strings.Count(capturedQuery, "is:issue") > 1 {
				t.Errorf("is:issue duplicated in query: %q", capturedQuery)
			}
		})
	}
}

func TestGitHubHookClient_Mock(t *testing.T) {
	hookID := int64(12345)
	deliveryID := int64(67890)
	guid := "test-guid"

	mockClient := &prober.MockGitHubHookClient{
		ListHooksFunc: func(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, error) {
			if owner != "brotherlogic" || repo != "ghwebhook" {
				t.Errorf("unexpected repo: %s/%s", owner, repo)
			}
			return []*github.Hook{
				{
					ID: &hookID,
				},
			}, nil
		},
		ListHookDeliveriesFunc: func(ctx context.Context, owner, repo string, hID int64, opts *github.ListCursorOptions) ([]*github.HookDelivery, error) {
			if hID != hookID {
				t.Errorf("unexpected hook ID: %d", hID)
			}
			return []*github.HookDelivery{
				{
					ID:   &deliveryID,
					GUID: &guid,
				},
			}, nil
		},
	}

	var client prober.GitHubHookClient = mockClient

	// Verify ListHooks
	hooks, err := client.ListHooks(context.Background(), "brotherlogic", "ghwebhook", nil)
	if err != nil {
		t.Fatalf("ListHooks failed: %v", err)
	}
	if len(hooks) != 1 || hooks[0].GetID() != hookID {
		t.Fatalf("unexpected hooks result: %+v", hooks)
	}

	// Verify ListHookDeliveries
	deliveries, err := client.ListHookDeliveries(context.Background(), "brotherlogic", "ghwebhook", hookID, nil)
	if err != nil {
		t.Fatalf("ListHookDeliveries failed: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].GetID() != deliveryID || deliveries[0].GetGUID() != guid {
		t.Fatalf("unexpected deliveries result: %+v", deliveries)
	}
}

func TestGitHubHookClient_Mock_NilFuncs(t *testing.T) {
	mockClient := &prober.MockGitHubHookClient{}
	hooks, err := mockClient.ListHooks(context.Background(), "owner", "repo", nil)
	if err != nil || hooks != nil {
		t.Errorf("expected nil result and nil error when ListHooksFunc is nil")
	}
	deliveries, err := mockClient.ListHookDeliveries(context.Background(), "owner", "repo", 123, nil)
	if err != nil || deliveries != nil {
		t.Errorf("expected nil result and nil error when ListHookDeliveriesFunc is nil")
	}
}

func TestNewDefaultGitHubHookClient_TokenResolution(t *testing.T) {
	// 1. Explicit token
	c1 := prober.NewDefaultGitHubHookClient("explicit-token")
	if c1 == nil {
		t.Fatal("expected non-nil hook client with explicit token")
	}

	// 2. Fallback to GH_TOKEN
	t.Setenv("GH_TOKEN", "gh-env-token")
	t.Setenv("GITHUB_TOKEN", "")
	c2 := prober.NewDefaultGitHubHookClient("")
	if c2 == nil {
		t.Fatal("expected non-nil hook client with GH_TOKEN")
	}

	// 3. Fallback to GITHUB_TOKEN
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "github-env-token")
	c3 := prober.NewDefaultGitHubHookClient("")
	if c3 == nil {
		t.Fatal("expected non-nil hook client with GITHUB_TOKEN")
	}

	// 4. Unauthenticated fallback
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	c4 := prober.NewDefaultGitHubHookClient("")
	if c4 == nil {
		t.Fatal("expected non-nil hook client with empty token")
	}
}

func TestNewGitHubHookClientFromClient(t *testing.T) {
	rawClient := github.NewClient(nil)
	c := prober.NewGitHubHookClientFromClient(rawClient)
	if c == nil {
		t.Fatal("expected non-nil hook client from existing *github.Client")
	}
}

func TestDefaultGitHubHookClient_LiveHttpEndpoints(t *testing.T) {
	hookID := int64(9876)
	deliveryID := int64(54321)
	guid := "delivery-guid-xyz"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/brotherlogic/ghwebhook/hooks":
			page := r.URL.Query().Get("page")
			perPage := r.URL.Query().Get("per_page")
			if page != "2" || perPage != "10" {
				t.Errorf("unexpected query params: page=%s, per_page=%s", page, perPage)
			}
			hooks := []*github.Hook{
				{
					ID:   github.Ptr(hookID),
					Name: github.Ptr("web"),
				},
			}
			_ = json.NewEncoder(w).Encode(hooks)

		case r.Method == http.MethodGet && r.URL.Path == "/repos/brotherlogic/ghwebhook/hooks/9876/deliveries":
			cursor := r.URL.Query().Get("cursor")
			if cursor != "cursor123" {
				t.Errorf("unexpected cursor: %s", cursor)
			}
			deliveries := []*github.HookDelivery{
				{
					ID:   github.Ptr(deliveryID),
					GUID: github.Ptr(guid),
				},
			}
			_ = json.NewEncoder(w).Encode(deliveries)

		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	gh := github.NewClient(ts.Client())
	gh.BaseURL, _ = gh.BaseURL.Parse(ts.URL + "/")

	client := prober.NewGitHubHookClientFromClient(gh)
	ctx := context.Background()

	// 1. ListHooks
	hooks, err := client.ListHooks(ctx, "brotherlogic", "ghwebhook", &github.ListOptions{
		Page:    2,
		PerPage: 10,
	})
	if err != nil {
		t.Fatalf("ListHooks failed: %v", err)
	}
	if len(hooks) != 1 || hooks[0].GetID() != hookID {
		t.Fatalf("unexpected hooks result: %+v", hooks)
	}

	// 2. ListHookDeliveries
	deliveries, err := client.ListHookDeliveries(ctx, "brotherlogic", "ghwebhook", hookID, &github.ListCursorOptions{
		Cursor: "cursor123",
	})
	if err != nil {
		t.Fatalf("ListHookDeliveries failed: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].GetID() != deliveryID || deliveries[0].GetGUID() != guid {
		t.Fatalf("unexpected deliveries result: %+v", deliveries)
	}
}

