package server

import (
	"context"

	"github.com/google/go-github/v69/github"
)

// GitHubHookClient defines the interface for interacting with GitHub repository webhooks.
type GitHubHookClient interface {
	ListHooks(ctx context.Context, owner, repo string) ([]*github.Hook, error)
	DeleteHook(ctx context.Context, owner, repo string, hookID int64) error
}

type defaultGitHubHookClient struct {
	client *github.Client
}

// NewDefaultGitHubHookClient creates a GitHubHookClient authenticated with the given token (if provided).
func NewDefaultGitHubHookClient(token string) GitHubHookClient {
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

func (d *defaultGitHubHookClient) ListHooks(ctx context.Context, owner, repo string) ([]*github.Hook, error) {
	hooks, _, err := d.client.Repositories.ListHooks(ctx, owner, repo, nil)
	return hooks, err
}

func (d *defaultGitHubHookClient) DeleteHook(ctx context.Context, owner, repo string, hookID int64) error {
	_, err := d.client.Repositories.DeleteHook(ctx, owner, repo, hookID)
	return err
}
