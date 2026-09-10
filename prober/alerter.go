package prober

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v69/github"
)

// Alert constants for prober failure reporting.
const (
	DefaultAlertTitle       = "[PROBER FAILURE] Webhook delivery or validation failed"
	DefaultAlertLabelBug    = "bug"
	DefaultAlertLabelProber = "prober-failure"
)

// AlertResult represents the outcome of the failure alerting process.
type AlertResult struct {
	Deduplicated bool
	IssueNumber  int
	IssueURL     string
}

// HandleHardFailure processes a hard failure result from a prober run.
// It searches for existing open alert issues in the target repository matching DefaultAlertTitle.
// If an open matching issue exists, it deduplicates and returns without creating a new issue.
// If no matching open issue exists, it creates a new GitHub issue with diagnostic information and default labels.
func HandleHardFailure(ctx context.Context, client GitHubIssueClient, repoFullName string, res Result) (*AlertResult, error) {
	parts := strings.Split(repoFullName, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid repository full name %q: expected owner/repo", repoFullName)
	}
	owner, repo := parts[0], parts[1]

	// Search for existing open alert issues
	query := fmt.Sprintf("is:issue is:open in:title %q", DefaultAlertTitle)
	issues, err := client.SearchIssues(ctx, owner, repo, query)
	if err != nil {
		return nil, fmt.Errorf("failed to search existing alert issues: %w", err)
	}

	// Verify exact title match and ensure the issue is not closed
	for _, issue := range issues {
		if issue.GetState() != "closed" && issue.GetTitle() == DefaultAlertTitle {
			return &AlertResult{
				Deduplicated: true,
				IssueNumber:  issue.GetNumber(),
				IssueURL:     issue.GetHTMLURL(),
			}, nil
		}
	}

	// Construct diagnostic Markdown report
	body := buildDiagnosticReport(repoFullName, res)

	title := DefaultAlertTitle
	labels := []string{DefaultAlertLabelBug, DefaultAlertLabelProber}
	req := &github.IssueRequest{
		Title:  &title,
		Body:   &body,
		Labels: &labels,
	}

	created, err := client.CreateIssue(ctx, owner, repo, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create alert issue: %w", err)
	}

	return &AlertResult{
		Deduplicated: false,
		IssueNumber:  created.GetNumber(),
		IssueURL:     created.GetHTMLURL(),
	}, nil
}

func buildDiagnosticReport(repoFullName string, res Result) string {
	timestampUTC := time.Now().UTC().Format(time.RFC3339)

	issueInfo := "N/A"
	if res.IssueNumber > 0 {
		issueInfo = fmt.Sprintf("#%d", res.IssueNumber)
	}
	action := res.Action
	if action == "" {
		action = "N/A"
	}

	errMsg := res.Message
	if errMsg == "" && res.Err != nil {
		errMsg = res.Err.Error()
	}
	if errMsg == "" {
		errMsg = "Hard failure encountered during webhook delivery or validation"
	}

	return fmt.Sprintf(`## 🚨 Prober Hard Failure Alert

The automated prober encountered a hard failure while verifying webhook delivery and validation.

### Diagnostic Report

- **Timestamp:** %s (UTC)
- **Target Repository:** %s
- **Probe Execution Duration:** %s
- **Triggered Test Issue:** %s (Action: %s)
- **Status:** %s
- **Failure Details:**
`+"```"+`
%s
`+"```"+`

### Operator Troubleshooting Steps

1. **Check Service Health:** Verify if ghwebhook is healthy and accessible at its configured endpoint.
2. **Review GitHub Webhook Deliveries:** In GitHub repository settings, check the Webhooks delivery log for recent failures, HTTP response codes, or timeouts.
3. **Inspect Server Logs:** Check ghwebhook container logs for HMAC validation failures or gRPC forwarding errors.
4. **Network & DNS:** Verify internal DNS resolution and firewall rules between ghwebhook and registered services.
5. **Resolution:** After identifying and fixing the underlying failure, run the prober again or close this alert issue.
`, timestampUTC, repoFullName, res.Duration.String(), issueInfo, action, res.Status.String(), errMsg)
}
