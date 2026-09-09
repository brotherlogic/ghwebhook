package prober_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brotherlogic/ghwebhook/prober"
	"github.com/google/go-github/v69/github"
)

func TestHandleHardFailure_ExistingOpenIssue_Deduplicated(t *testing.T) {
	ctx := context.Background()
	existingNum := 123
	existingURL := "https://github.com/brotherlogic/ghwebhook/issues/123"
	stateOpen := "open"
	alertTitle := prober.DefaultAlertTitle

	createCalled := false
	mockClient := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			if owner != "brotherlogic" || repo != "ghwebhook" {
				t.Errorf("SearchIssues unexpected repo: %s/%s", owner, repo)
			}
			expectedQuery := fmt.Sprintf("is:issue is:open in:title %q", alertTitle)
			if query != expectedQuery {
				t.Errorf("SearchIssues query mismatch: got %q, want %q", query, expectedQuery)
			}
			return []*github.Issue{
				{
					Number:  &existingNum,
					HTMLURL: &existingURL,
					Title:   &alertTitle,
					State:   &stateOpen,
				},
			}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			createCalled = true
			return nil, errors.New("CreateIssue should not be called when open issue exists")
		},
	}

	res := prober.Result{
		Status:      prober.StatusHardFailure,
		Duration:    45 * time.Second,
		IssueNumber: 999,
		Action:      "opened",
		Message:     "webhook delivery timed out waiting for event",
		Err:         errors.New("timeout"),
	}

	alertRes, err := prober.HandleHardFailure(ctx, mockClient, "brotherlogic/ghwebhook", res)
	if err != nil {
		t.Fatalf("HandleHardFailure failed unexpectedly: %v", err)
	}

	if createCalled {
		t.Errorf("CreateIssue was called unexpectedly")
	}

	if alertRes == nil {
		t.Fatalf("expected non-nil AlertResult")
	}

	if !alertRes.Deduplicated {
		t.Errorf("expected Deduplicated to be true, got false")
	}

	if alertRes.IssueNumber != existingNum {
		t.Errorf("expected IssueNumber %d, got %d", existingNum, alertRes.IssueNumber)
	}

	if alertRes.IssueURL != existingURL {
		t.Errorf("expected IssueURL %q, got %q", existingURL, alertRes.IssueURL)
	}
}

func TestHandleHardFailure_NoOpenIssue_CreatesIssue(t *testing.T) {
	ctx := context.Background()
	newNum := 789
	newURL := "https://github.com/brotherlogic/ghwebhook/issues/789"
	alertTitle := prober.DefaultAlertTitle
	stateOpen := "open"

	createCalled := false
	var capturedReq *github.IssueRequest

	mockClient := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			createCalled = true
			capturedReq = req
			if owner != "brotherlogic" || repo != "ghwebhook" {
				t.Errorf("CreateIssue unexpected repo: %s/%s", owner, repo)
			}
			return &github.Issue{
				Number:  &newNum,
				HTMLURL: &newURL,
				Title:   req.Title,
				State:   &stateOpen,
			}, nil
		},
	}

	res := prober.Result{
		Status:      prober.StatusHardFailure,
		Duration:    52 * time.Second,
		IssueNumber: 456,
		Action:      "reopened",
		Message:     "payload validation failed: mismatched issue number",
		Err:         errors.New("validation failed"),
	}

	alertRes, err := prober.HandleHardFailure(ctx, mockClient, "brotherlogic/ghwebhook", res)
	if err != nil {
		t.Fatalf("HandleHardFailure failed unexpectedly: %v", err)
	}

	if !createCalled {
		t.Fatalf("expected CreateIssue to be called")
	}

	if capturedReq.GetTitle() != alertTitle {
		t.Errorf("expected title %q, got %q", alertTitle, capturedReq.GetTitle())
	}

	labels := capturedReq.GetLabels()
	hasBug := false
	hasProber := false
	for _, l := range labels {
		if l == prober.DefaultAlertLabelBug {
			hasBug = true
		}
		if l == prober.DefaultAlertLabelProber {
			hasProber = true
		}
	}
	if !hasBug || !hasProber {
		t.Errorf("expected labels [%s, %s], got %v", prober.DefaultAlertLabelBug, prober.DefaultAlertLabelProber, labels)
	}

	body := capturedReq.GetBody()
	expectedSubstrings := []string{
		"UTC",
		"brotherlogic/ghwebhook",
		"52s",
		"456",
		"reopened",
		"payload validation failed: mismatched issue number",
		"Troubleshooting",
	}
	for _, sub := range expectedSubstrings {
		if !strings.Contains(body, sub) {
			t.Errorf("expected body to contain %q, but body was:\n%s", sub, body)
		}
	}

	if alertRes == nil {
		t.Fatalf("expected non-nil AlertResult")
	}

	if alertRes.Deduplicated {
		t.Errorf("expected Deduplicated to be false, got true")
	}

	if alertRes.IssueNumber != newNum {
		t.Errorf("expected IssueNumber %d, got %d", newNum, alertRes.IssueNumber)
	}

	if alertRes.IssueURL != newURL {
		t.Errorf("expected IssueURL %q, got %q", newURL, alertRes.IssueURL)
	}
}

func TestHandleHardFailure_ClosedIssueIgnored_CreatesNew(t *testing.T) {
	ctx := context.Background()
	closedNum := 100
	closedURL := "https://github.com/brotherlogic/ghwebhook/issues/100"
	stateClosed := "closed"
	alertTitle := prober.DefaultAlertTitle

	newNum := 200
	newURL := "https://github.com/brotherlogic/ghwebhook/issues/200"
	stateOpen := "open"

	createCalled := false
	mockClient := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			// Returns a closed issue with matching title - should be ignored
			return []*github.Issue{
				{
					Number:  &closedNum,
					HTMLURL: &closedURL,
					Title:   &alertTitle,
					State:   &stateClosed,
				},
			}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			createCalled = true
			return &github.Issue{
				Number:  &newNum,
				HTMLURL: &newURL,
				Title:   req.Title,
				State:   &stateOpen,
			}, nil
		},
	}

	res := prober.Result{
		Status:      prober.StatusHardFailure,
		Duration:    60 * time.Second,
		IssueNumber: 12,
		Action:      "opened",
		Message:     "timeout waiting for webhook",
	}

	alertRes, err := prober.HandleHardFailure(ctx, mockClient, "brotherlogic/ghwebhook", res)
	if err != nil {
		t.Fatalf("HandleHardFailure failed unexpectedly: %v", err)
	}

	if !createCalled {
		t.Fatalf("expected CreateIssue to be called because existing issue is closed")
	}

	if alertRes.Deduplicated {
		t.Errorf("expected Deduplicated to be false, got true")
	}

	if alertRes.IssueNumber != newNum {
		t.Errorf("expected IssueNumber %d, got %d", newNum, alertRes.IssueNumber)
	}
}

func TestHandleHardFailure_SearchError_Propagated(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("search API unavailable")

	mockClient := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return nil, expectedErr
		},
	}

	res := prober.Result{
		Status:   prober.StatusHardFailure,
		Duration: 10 * time.Second,
	}

	alertRes, err := prober.HandleHardFailure(ctx, mockClient, "brotherlogic/ghwebhook", res)
	if err == nil {
		t.Fatalf("expected error from HandleHardFailure, got nil")
	}

	if !strings.Contains(err.Error(), expectedErr.Error()) {
		t.Errorf("expected error to contain %q, got %v", expectedErr.Error(), err)
	}

	if alertRes != nil {
		t.Errorf("expected nil AlertResult on error, got %v", alertRes)
	}
}

func TestHandleHardFailure_CreateError_Propagated(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("failed to create issue: rate limit exceeded")

	mockClient := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			return nil, expectedErr
		},
	}

	res := prober.Result{
		Status:   prober.StatusHardFailure,
		Duration: 10 * time.Second,
	}

	alertRes, err := prober.HandleHardFailure(ctx, mockClient, "brotherlogic/ghwebhook", res)
	if err == nil {
		t.Fatalf("expected error from HandleHardFailure, got nil")
	}

	if !strings.Contains(err.Error(), expectedErr.Error()) {
		t.Errorf("expected error to contain %q, got %v", expectedErr.Error(), err)
	}

	if alertRes != nil {
		t.Errorf("expected nil AlertResult on error, got %v", alertRes)
	}
}

func TestHandleHardFailure_InvalidRepoFullName(t *testing.T) {
	ctx := context.Background()
	mockClient := &prober.MockGitHubIssueClient{}

	res := prober.Result{
		Status:   prober.StatusHardFailure,
		Duration: 10 * time.Second,
	}

	_, err := prober.HandleHardFailure(ctx, mockClient, "invalidrepo", res)
	if err == nil {
		t.Fatalf("expected error for invalid repo format, got nil")
	}
}
