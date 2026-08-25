package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brotherlogic/ghwebhook/server"
	pstore_client "github.com/brotherlogic/pstore/client"
	"github.com/google/go-github/v69/github"
)

type mockGitHubHookClient struct {
	listHooksFunc  func(ctx context.Context, owner, repo string) ([]*github.Hook, error)
	deleteHookFunc func(ctx context.Context, owner, repo string, hookID int64) error
}

func (m *mockGitHubHookClient) ListHooks(ctx context.Context, owner, repo string) ([]*github.Hook, error) {
	if m.listHooksFunc != nil {
		return m.listHooksFunc(ctx, owner, repo)
	}
	return nil, nil
}

func (m *mockGitHubHookClient) DeleteHook(ctx context.Context, owner, repo string, hookID int64) error {
	if m.deleteHookFunc != nil {
		return m.deleteHookFunc(ctx, owner, repo, hookID)
	}
	return nil
}

func TestGitHubHookClient_Mock(t *testing.T) {
	var client server.GitHubHookClient = &mockGitHubHookClient{
		listHooksFunc: func(ctx context.Context, owner, repo string) ([]*github.Hook, error) {
			id := int64(12345)
			url := "https://example.com/webhook"
			return []*github.Hook{
				{
					ID: &id,
					Config: &github.HookConfig{
						URL: &url,
					},
				},
			}, nil
		},
		deleteHookFunc: func(ctx context.Context, owner, repo string, hookID int64) error {
			if hookID != 12345 {
				t.Errorf("unexpected hookID: %d", hookID)
			}
			return nil
		},
	}

	hooks, err := client.ListHooks(context.Background(), "owner", "repo")
	if err != nil {
		t.Fatalf("ListHooks failed: %v", err)
	}
	if len(hooks) != 1 || *hooks[0].ID != 12345 {
		t.Errorf("unexpected hooks result: %+v", hooks)
	}

	err = client.DeleteHook(context.Background(), "owner", "repo", 12345)
	if err != nil {
		t.Fatalf("DeleteHook failed: %v", err)
	}
}

func TestNewDefaultGitHubHookClient(t *testing.T) {
	client := server.NewDefaultGitHubHookClient("fake-token")
	if client == nil {
		t.Fatal("expected non-nil GitHubHookClient")
	}

	emptyClient := server.NewDefaultGitHubHookClient("")
	if emptyClient == nil {
		t.Fatal("expected non-nil GitHubHookClient even with empty token")
	}
}

func TestServerOptions_GitHubClientAndIngressURL(t *testing.T) {
	pstore := pstore_client.GetTestClient()
	mockClient := &mockGitHubHookClient{}
	ingressURL := "https://ghwebhook.example.com/ingress"

	s := server.NewServer(
		pstore,
		server.WithGitHubClient(mockClient),
		server.WithIngressURL(ingressURL),
	)

	if s == nil {
		t.Fatal("expected non-nil Server")
	}

	if s.GetGitHubClient() != mockClient {
		t.Errorf("expected GitHubClient to match mockClient, got %v", s.GetGitHubClient())
	}

	if s.GetIngressURL() != ingressURL {
		t.Errorf("expected IngressURL to be %q, got %q", ingressURL, s.GetIngressURL())
	}
}

func TestDefaultGitHubHookClient_LiveEndpoints(t *testing.T) {
	// Test mock HTTP server for GitHub client
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/testowner/testrepo/hooks" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"id": 42, "config": {"url": "https://example.com/hook"}}]`))
			return
		}
		if r.URL.Path == "/repos/testowner/testrepo/hooks/42" && r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	gh := github.NewClient(ts.Client())
	gh.BaseURL, _ = gh.BaseURL.Parse(ts.URL + "/")

	client := server.NewGitHubHookClientFromClient(gh)
	hooks, err := client.ListHooks(context.Background(), "testowner", "testrepo")
	if err != nil {
		t.Fatalf("ListHooks failed: %v", err)
	}
	if len(hooks) != 1 || *hooks[0].ID != 42 {
		t.Fatalf("unexpected hooks response: %+v", hooks)
	}

	err = client.DeleteHook(context.Background(), "testowner", "testrepo", 42)
	if err != nil {
		t.Fatalf("DeleteHook failed: %v", err)
	}
}
