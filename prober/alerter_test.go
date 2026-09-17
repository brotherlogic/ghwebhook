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

func TestBuildDiagnosticReport_FallbackNilDiagnostics(t *testing.T) {
	res := prober.Result{
		Status:      prober.StatusHardFailure,
		Duration:    30 * time.Second,
		IssueNumber: 101,
		Action:      "opened",
		Message:     "webhook delivery timed out",
		Diagnostics: nil,
	}

	body := prober.BuildDiagnosticReport("brotherlogic/ghwebhook", res)

	// Check backwards-compatible standard elements
	if !strings.Contains(body, "## 🚨 Prober Hard Failure Alert") {
		t.Errorf("expected alert heading in report")
	}
	if !strings.Contains(body, "### Diagnostic Report") {
		t.Errorf("expected diagnostic report heading")
	}
	if !strings.Contains(body, "brotherlogic/ghwebhook") {
		t.Errorf("expected repo full name in report")
	}
	if !strings.Contains(body, "#101") {
		t.Errorf("expected issue number in report")
	}
	if !strings.Contains(body, "### Operator Troubleshooting Steps") {
		t.Errorf("expected standard troubleshooting steps")
	}
	if strings.Contains(body, "Root Cause:") {
		t.Errorf("expected no root cause heading when diagnostics is nil")
	}
	if strings.Contains(body, "| Hook ID |") {
		t.Errorf("expected no delivery table when diagnostics is nil")
	}
}

func TestBuildDiagnosticReport_RootCauseWebhookMissing(t *testing.T) {
	res := prober.Result{
		Status:      prober.StatusHardFailure,
		Duration:    60 * time.Second,
		IssueNumber: 102,
		Action:      "opened",
		Message:     "timed out waiting for webhook event",
		Diagnostics: &prober.InspectionDiagnostics{
			RootCause:        prober.RootCauseWebhookMissing,
			RootCauseDetail:  "no active webhook configured for issue events found on repository",
			ActiveHooksCount: 0,
		},
	}

	body := prober.BuildDiagnosticReport("brotherlogic/ghwebhook", res)

	if !strings.Contains(body, "Root Cause: Webhook Missing") {
		t.Errorf("expected Root Cause heading with Webhook Missing, got:\n%s", body)
	}
	if !strings.Contains(body, "repository webhook configuration") && !strings.Contains(body, "repository webhook") {
		t.Errorf("expected guidance to verify repository webhook configuration, got:\n%s", body)
	}
	if strings.Contains(body, "| Hook ID |") {
		t.Errorf("expected no delivery table when no active hooks, got:\n%s", body)
	}
}

func TestBuildDiagnosticReport_RootCauseDeliveryFailed(t *testing.T) {
	deliveredAt := time.Date(2026, 9, 17, 16, 30, 0, 0, time.UTC)
	res := prober.Result{
		Status:      prober.StatusHardFailure,
		Duration:    60 * time.Second,
		IssueNumber: 103,
		Action:      "reopened",
		Message:     "timed out waiting for webhook event",
		Diagnostics: &prober.InspectionDiagnostics{
			RootCause:        prober.RootCauseDeliveryFailed,
			RootCauseDetail:  "webhook delivery failed with HTTP status 502: Bad Gateway",
			ActiveHooksCount: 1,
			MatchingDeliveries: []prober.HookDeliverySummary{
				{
					HookID:      123456,
					DeliveryID:  789012,
					GUID:        "guid-delivery-502",
					DeliveredAt: deliveredAt,
					StatusCode:  502,
					Status:      "Bad Gateway",
					Duration:    0.145,
					Event:       "issues",
					Action:      "reopened",
				},
			},
		},
	}

	body := prober.BuildDiagnosticReport("brotherlogic/ghwebhook", res)

	if !strings.Contains(body, "Root Cause: GitHub Delivery Failed") {
		t.Errorf("expected Root Cause heading with GitHub Delivery Failed, got:\n%s", body)
	}
	// Delivery table check
	tableHeaders := []string{"Hook ID", "Delivery GUID", "Delivered At", "HTTP Status", "Status Message", "Duration"}
	for _, h := range tableHeaders {
		if !strings.Contains(body, h) {
			t.Errorf("expected delivery table header %q in body, got:\n%s", h, body)
		}
	}
	if !strings.Contains(body, "123456") || !strings.Contains(body, "guid-delivery-502") || !strings.Contains(body, "502") || !strings.Contains(body, "Bad Gateway") {
		t.Errorf("expected delivery row fields in table, got:\n%s", body)
	}

	// Guidance checks
	requiredGuidance := []string{"ingress routing", "TLS certificates", "firewall", "ingress gateway logs"}
	for _, g := range requiredGuidance {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(g)) {
			t.Errorf("expected operator guidance to contain %q, got:\n%s", g, body)
		}
	}
}

func TestBuildDiagnosticReport_RootCauseLostInRouting(t *testing.T) {
	deliveredAt := time.Date(2026, 9, 17, 16, 31, 0, 0, time.UTC)
	res := prober.Result{
		Status:      prober.StatusHardFailure,
		Duration:    60 * time.Second,
		IssueNumber: 104,
		Action:      "reopened",
		Message:     "timed out waiting for webhook event",
		Diagnostics: &prober.InspectionDiagnostics{
			RootCause:        prober.RootCauseLostInRouting,
			RootCauseDetail:  "webhook delivered by GitHub (HTTP 200) but was not received by handler",
			ActiveHooksCount: 1,
			MatchingDeliveries: []prober.HookDeliverySummary{
				{
					HookID:      123456,
					DeliveryID:  789013,
					GUID:        "guid-delivery-200",
					DeliveredAt: deliveredAt,
					StatusCode:  200,
					Status:      "OK",
					Duration:    0.025,
					Event:       "issues",
					Action:      "reopened",
				},
			},
		},
	}

	body := prober.BuildDiagnosticReport("brotherlogic/ghwebhook", res)

	if !strings.Contains(body, "Root Cause: Delivered by GitHub but Lost in Routing") {
		t.Errorf("expected Root Cause heading with Lost in Routing, got:\n%s", body)
	}
	if !strings.Contains(body, "guid-delivery-200") || !strings.Contains(body, "200") {
		t.Errorf("expected delivery details in table, got:\n%s", body)
	}

	// Guidance checks
	requiredGuidance := []string{"proxy HMAC verification", "gRPC handler connectivity", "service registration"}
	for _, g := range requiredGuidance {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(g)) {
			t.Errorf("expected operator guidance to contain %q, got:\n%s", g, body)
		}
	}
}

func TestBuildDiagnosticReport_RootCauseNoDeliveryAttempted(t *testing.T) {
	res := prober.Result{
		Status:      prober.StatusHardFailure,
		Duration:    60 * time.Second,
		IssueNumber: 105,
		Action:      "closed",
		Message:     "timed out waiting for webhook event",
		Diagnostics: &prober.InspectionDiagnostics{
			RootCause:          prober.RootCauseNoDeliveryAttempted,
			RootCauseDetail:    "active webhook exists but no delivery attempt was recorded",
			ActiveHooksCount:   1,
			MatchingDeliveries: []prober.HookDeliverySummary{},
		},
	}

	body := prober.BuildDiagnosticReport("brotherlogic/ghwebhook", res)

	if !strings.Contains(body, "Root Cause: No Delivery Attempted") {
		t.Errorf("expected Root Cause heading with No Delivery Attempted, got:\n%s", body)
	}
	if !strings.Contains(strings.ToLower(body), "upstream github event processing status") {
		t.Errorf("expected operator guidance to contain 'upstream GitHub event processing status', got:\n%s", body)
	}
}

func TestBuildDiagnosticReport_RootCauseInspectionUnavailable(t *testing.T) {
	res := prober.Result{
		Status:      prober.StatusHardFailure,
		Duration:    60 * time.Second,
		IssueNumber: 106,
		Action:      "opened",
		Message:     "timed out waiting for webhook event",
		Diagnostics: &prober.InspectionDiagnostics{
			RootCause:       prober.RootCauseInspectionUnavailable,
			RootCauseDetail: "insufficient token permissions (admin:repo_hook required)",
			ErrorMessage:    "GET https://api.github.com/repos/brotherlogic/ghwebhook/hooks: 403 Must have admin rights to Repository",
		},
	}

	body := prober.BuildDiagnosticReport("brotherlogic/ghwebhook", res)

	if !strings.Contains(body, "Root Cause: Inspection Unavailable") {
		t.Errorf("expected Root Cause heading with Inspection Unavailable, got:\n%s", body)
	}
	if !strings.Contains(body, "admin:repo_hook") {
		t.Errorf("expected warning or guidance mentioning admin:repo_hook token permissions, got:\n%s", body)
	}
}

func TestHandleHardFailure_WithDiagnostics_IncludesEnrichedReport(t *testing.T) {
	ctx := context.Background()
	newNum := 999
	newURL := "https://github.com/brotherlogic/ghwebhook/issues/999"
	stateOpen := "open"

	var capturedBody string
	mockClient := &prober.MockGitHubIssueClient{
		SearchIssuesFunc: func(ctx context.Context, owner, repo, query string) ([]*github.Issue, error) {
			return []*github.Issue{}, nil
		},
		CreateIssueFunc: func(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error) {
			capturedBody = req.GetBody()
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
		IssueNumber: 107,
		Action:      "reopened",
		Message:     "timed out waiting for webhook event",
		Diagnostics: &prober.InspectionDiagnostics{
			RootCause:        prober.RootCauseLostInRouting,
			RootCauseDetail:  "delivered by GitHub (HTTP 200) but lost in routing",
			ActiveHooksCount: 1,
			MatchingDeliveries: []prober.HookDeliverySummary{
				{
					HookID:      555,
					DeliveryID:  777,
					GUID:        "guid-12345",
					DeliveredAt: time.Now().UTC(),
					StatusCode:  200,
					Status:      "OK",
					Duration:    0.05,
				},
			},
		},
	}

	alertRes, err := prober.HandleHardFailure(ctx, mockClient, "brotherlogic/ghwebhook", res)
	if err != nil {
		t.Fatalf("HandleHardFailure failed: %v", err)
	}
	if alertRes == nil || alertRes.IssueNumber != newNum {
		t.Fatalf("unexpected alert result: %v", alertRes)
	}
	if !strings.Contains(capturedBody, "Root Cause: Delivered by GitHub but Lost in Routing") {
		t.Errorf("expected captured body to contain Root Cause, got:\n%s", capturedBody)
	}
	if !strings.Contains(capturedBody, "guid-12345") {
		t.Errorf("expected captured body to contain delivery table GUID, got:\n%s", capturedBody)
	}
}
