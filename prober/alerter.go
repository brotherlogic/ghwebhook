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
	DefaultAlertTitle         = "[PROBER FAILURE] Webhook delivery or validation failed"
	DefaultAlertLabelBug      = "bug"
	DefaultAlertLabelProber   = "prober-failure"
	DefaultAlertLabelAnalysis = "seraphine-bug-analysis"
	DefaultAlertAssignee      = "brotherlogic-automation"
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
	labels := []string{DefaultAlertLabelBug, DefaultAlertLabelProber, DefaultAlertLabelAnalysis}
	assignees := []string{DefaultAlertAssignee}
	req := &github.IssueRequest{
		Title:     &title,
		Body:      &body,
		Labels:    &labels,
		Assignees: &assignees,
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

// BuildDiagnosticReport generates the formatted Markdown diagnostic report for a prober run result.
func BuildDiagnosticReport(repoFullName string, res Result) string {
	return buildDiagnosticReport(repoFullName, res)
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

	if res.Diagnostics == nil {
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

	var sb strings.Builder
	sb.WriteString("## 🚨 Prober Hard Failure Alert\n\n")
	sb.WriteString("The automated prober encountered a hard failure while verifying webhook delivery and validation.\n\n")
	sb.WriteString("### Diagnostic Report\n\n")
	sb.WriteString(fmt.Sprintf("- **Timestamp:** %s (UTC)\n", timestampUTC))
	sb.WriteString(fmt.Sprintf("- **Target Repository:** %s\n", repoFullName))
	sb.WriteString(fmt.Sprintf("- **Probe Execution Duration:** %s\n", res.Duration.String()))
	sb.WriteString(fmt.Sprintf("- **Triggered Test Issue:** %s (Action: %s)\n", issueInfo, action))
	sb.WriteString(fmt.Sprintf("- **Status:** %s\n", res.Status.String()))
	sb.WriteString(fmt.Sprintf("- **Root Cause:** %s\n", string(res.Diagnostics.RootCause)))
	sb.WriteString("- **Failure Details:**\n```\n")
	sb.WriteString(errMsg)
	sb.WriteString("\n```\n\n")

	sb.WriteString(fmt.Sprintf("### Root Cause: %s\n\n", string(res.Diagnostics.RootCause)))
	if res.Diagnostics.RootCauseDetail != "" {
		sb.WriteString(fmt.Sprintf("%s\n\n", res.Diagnostics.RootCauseDetail))
	}
	if res.Diagnostics.RootCause == RootCauseInspectionUnavailable {
		sb.WriteString("> ⚠️ **Warning:** Missing `admin:repo_hook` token permissions; delivery inspection could not be completed.\n\n")
		if res.Diagnostics.ErrorMessage != "" {
			sb.WriteString(fmt.Sprintf("- **Inspection Error:** `%s`\n\n", res.Diagnostics.ErrorMessage))
		}
	}

	if len(res.Diagnostics.MatchingDeliveries) > 0 {
		sb.WriteString("### Webhook Deliveries\n\n")
		sb.WriteString("| Hook ID | Delivery GUID | Delivered At | HTTP Status | Status Message | Duration |\n")
		sb.WriteString("|---------|---------------|--------------|-------------|----------------|----------|\n")
		for _, d := range res.Diagnostics.MatchingDeliveries {
			deliveredAtStr := "N/A"
			if !d.DeliveredAt.IsZero() {
				deliveredAtStr = d.DeliveredAt.UTC().Format(time.RFC3339)
			}
			statusMsg := d.Status
			if statusMsg == "" {
				statusMsg = "N/A"
			}
			guid := d.GUID
			if guid == "" {
				guid = "N/A"
			}
			sb.WriteString(fmt.Sprintf("| %d | %s | %s | %d | %s | %.3fs |\n",
				d.HookID, guid, deliveredAtStr, d.StatusCode, statusMsg, d.Duration))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### Operator Troubleshooting Steps\n\n")
	sb.WriteString(renderTroubleshootingGuidance(res.Diagnostics.RootCause))
	sb.WriteString("\n")

	return sb.String()
}

func renderTroubleshootingGuidance(rc RootCause) string {
	switch rc {
	case RootCauseWebhookMissing:
		return `1. **Verify Repository Webhook Configuration:** Check repository settings to verify repository webhook configuration for ` + "`issues`" + ` events targeting the ingress endpoint.
2. **Check Webhook Secret:** Verify that the repository webhook secret matches the proxy's expected secret.
3. **Verify Service Health:** Verify if ghwebhook is healthy and accessible at its configured endpoint.`

	case RootCauseDeliveryFailed:
		return `1. **Check Ingress Routing & Gateway Logs:** Check ingress routing and ingress gateway logs for delivery failures or dropped connections.
2. **Verify TLS Certificates & Firewall:** Check TLS certificates validity and firewall rules to ensure GitHub webhook traffic can reach the endpoint.
3. **Inspect Server Logs:** Check ghwebhook container logs for HTTP error responses or connection reset errors.`

	case RootCauseLostInRouting:
		return `1. **Check Proxy HMAC Verification:** Check proxy HMAC verification (` + "`X-Hub-Signature-256`" + `) is succeeding on incoming requests.
2. **Check gRPC Handler Connectivity:** Check gRPC handler connectivity between the proxy and handler services.
3. **Verify Service Registration:** Check service registration status to ensure the repository is actively registered in pstore.`

	case RootCauseNoDeliveryAttempted:
		return `1. **Check Upstream GitHub Event Processing Status:** Check upstream GitHub event processing status and repository webhook recent delivery tabs for event delays or outages.
2. **Verify Event Trigger:** Ensure the test issue action was properly dispatched and eligible to trigger webhook events.
3. **Inspect Active Webhook Filters:** Verify that active webhooks on the repository are configured to listen to ` + "`issues`" + ` events.`

	case RootCauseInspectionUnavailable:
		return `1. **Verify Token Permissions:** Ensure prober token has ` + "`admin:repo_hook`" + ` token permissions to inspect webhook deliveries.
2. **Check Service Health:** Verify if ghwebhook is healthy and accessible at its configured endpoint.
3. **Review GitHub Webhook Deliveries:** In GitHub repository settings, check the Webhooks delivery log for recent failures, HTTP response codes, or timeouts.
4. **Inspect Server Logs:** Check ghwebhook container logs for HMAC validation failures or gRPC forwarding errors.
5. **Network & DNS:** Verify internal DNS resolution and firewall rules between ghwebhook and registered services.`

	default:
		return `1. **Check Service Health:** Verify if ghwebhook is healthy and accessible at its configured endpoint.
2. **Review GitHub Webhook Deliveries:** In GitHub repository settings, check the Webhooks delivery log for recent failures, HTTP response codes, or timeouts.
3. **Inspect Server Logs:** Check ghwebhook container logs for HMAC validation failures or gRPC forwarding errors.
4. **Network & DNS:** Verify internal DNS resolution and firewall rules between ghwebhook and registered services.
5. **Resolution:** After identifying and fixing the underlying failure, run the prober again or close this alert issue.`
	}
}
