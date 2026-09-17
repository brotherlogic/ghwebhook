package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/brotherlogic/ghwebhook/prober"
)

const (
	DefaultMetricsAddr        = ":8081"
	DefaultMetricsHoldTimeout = 30 * time.Second
)

var (
	recordResultFunc             = prober.RecordResult
	serveMetricsUntilScrapedFunc = prober.ServeMetricsUntilScraped
)

// Config encapsulates configuration parameters for the prober CLI.
type Config struct {
	Repo               string
	GHWebhookAddr      string
	ListenAddr         string
	ServiceAddr        string
	Timeout            time.Duration
	GitHubToken        string
	MetricsAddr        string
	MetricsHoldTimeout time.Duration
}

// parseConfig parses command-line arguments and falls back to environment variables.
func parseConfig(args []string, getenv func(string) string) (*Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	repo := getenv("PROBER_REPO")
	if repo == "" {
		repo = prober.DefaultRepo
	}

	ghwebhookAddr := getenv("PROBER_GHWEBHOOK_ADDR")
	if ghwebhookAddr == "" {
		ghwebhookAddr = prober.DefaultGHWebhookAddr
	}

	listenAddr := getenv("PROBER_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = prober.DefaultListenAddr
	}

	serviceAddr := getenv("PROBER_SERVICE_ADDR")
	if serviceAddr == "" {
		serviceAddr = prober.DefaultServiceAddr
	}

	timeoutStr := getenv("PROBER_TIMEOUT")
	timeout := prober.DefaultTimeout
	if timeoutStr != "" {
		d, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return nil, fmt.Errorf("invalid PROBER_TIMEOUT duration %q: %w", timeoutStr, err)
		}
		timeout = d
	}

	metricsAddr := getenv("PROBER_METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = DefaultMetricsAddr
	}

	metricsHoldTimeoutStr := getenv("PROBER_METRICS_HOLD_TIMEOUT")
	metricsHoldTimeout := DefaultMetricsHoldTimeout
	if metricsHoldTimeoutStr != "" {
		d, err := time.ParseDuration(metricsHoldTimeoutStr)
		if err != nil {
			return nil, fmt.Errorf("invalid PROBER_METRICS_HOLD_TIMEOUT duration %q: %w", metricsHoldTimeoutStr, err)
		}
		metricsHoldTimeout = d
	}

	token := getenv("GH_TOKEN")
	if token == "" {
		token = getenv("GITHUB_TOKEN")
	}

	fs := flag.NewFlagSet("prober", flag.ContinueOnError)
	fs.StringVar(&repo, "repo", repo, "Target repository full name (owner/repo)")
	fs.StringVar(&ghwebhookAddr, "ghwebhook-addr", ghwebhookAddr, "Address of ghwebhook registration gRPC service")
	fs.StringVar(&listenAddr, "listen-addr", listenAddr, "Local address for WebhookHandler gRPC server")
	fs.StringVar(&serviceAddr, "service-addr", serviceAddr, "Service address advertised to ghwebhook")
	fs.DurationVar(&timeout, "timeout", timeout, "Maximum execution timeout for the probe")
	fs.StringVar(&token, "github-token", token, "GitHub API token")
	fs.StringVar(&metricsAddr, "metrics-addr", metricsAddr, "Address for Prometheus metrics scrape server")
	fs.DurationVar(&metricsHoldTimeout, "metrics-hold-timeout", metricsHoldTimeout, "Maximum duration to hold and serve Prometheus metrics until scraped")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if token == "" {
		return nil, errors.New("github token is required (set via --github-token, GH_TOKEN, or GITHUB_TOKEN)")
	}

	return &Config{
		Repo:               repo,
		GHWebhookAddr:      ghwebhookAddr,
		ListenAddr:         listenAddr,
		ServiceAddr:        serviceAddr,
		Timeout:            timeout,
		GitHubToken:        token,
		MetricsAddr:        metricsAddr,
		MetricsHoldTimeout: metricsHoldTimeout,
	}, nil
}

// run executes the prober instance, emits structured logs, and returns the exit code.
func run(ctx context.Context, cfg *Config, p *prober.Prober, ghClient prober.GitHubIssueClient, repo string, stdout, stderr io.Writer) int {
	if ghClient == nil && p != nil {
		ghClient = p.GitHubClient()
	}
	if repo == "" && p != nil {
		repo = p.RepoFullName()
	}

	result, _ := p.Run(ctx)

	metricsAddr := DefaultMetricsAddr
	metricsHoldTimeout := DefaultMetricsHoldTimeout
	if cfg != nil {
		if cfg.MetricsAddr != "" {
			metricsAddr = cfg.MetricsAddr
		}
		if cfg.MetricsHoldTimeout > 0 {
			metricsHoldTimeout = cfg.MetricsHoldTimeout
		}
	}

	recordResultFunc(repo, result)

	if err := serveMetricsUntilScrapedFunc(ctx, metricsAddr, metricsHoldTimeout); err != nil {
		slog.ErrorContext(ctx, "failed to serve prober metrics",
			slog.String("error", err.Error()),
			slog.String("metrics_addr", metricsAddr),
		)
	}

	handler := slog.NewJSONHandler(stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)

	var errStr string
	if result.Err != nil {
		errStr = result.Err.Error()
	}

	switch result.Status {
	case prober.StatusSuccess:
		logger.Info("prober execution completed",
			slog.String("status", result.Status.String()),
			slog.Int("status_code", int(result.Status)),
			slog.String("action", result.Action),
			slog.Int("issue_number", result.IssueNumber),
			slog.Duration("duration", result.Duration),
			slog.String("message", result.Message),
			slog.String("error", errStr),
		)
		return 0

	case prober.StatusSoftFailure:
		logger.Warn("prober encountered soft failure (transient)",
			slog.String("status", result.Status.String()),
			slog.Int("status_code", int(result.Status)),
			slog.String("action", result.Action),
			slog.Int("issue_number", result.IssueNumber),
			slog.Duration("duration", result.Duration),
			slog.String("message", result.Message),
			slog.String("error", errStr),
		)
		return 0

	case prober.StatusHardFailure:
		logger.Error("prober encountered hard failure",
			slog.String("status", result.Status.String()),
			slog.Int("status_code", int(result.Status)),
			slog.String("action", result.Action),
			slog.Int("issue_number", result.IssueNumber),
			slog.Duration("duration", result.Duration),
			slog.String("message", result.Message),
			slog.String("error", errStr),
		)

		alertRes, alertErr := prober.HandleHardFailure(ctx, ghClient, repo, result)
		if alertErr != nil {
			logger.Error("failed to handle hard failure alert",
				slog.String("error", alertErr.Error()),
				slog.String("repo", repo),
			)
			fmt.Fprintf(stderr, "Error handling prober hard failure alert: %v\n", alertErr)
			return 1
		}

		logger.Info("prober hard failure alert handled",
			slog.Bool("deduplicated", alertRes.Deduplicated),
			slog.Int("alert_issue_number", alertRes.IssueNumber),
			slog.String("alert_issue_url", alertRes.IssueURL),
		)
		return 0

	default:
		return 1
	}
}

// runWithProber constructs a Prober from Config and executes it.
func runWithProber(ctx context.Context, cfg *Config, stdout, stderr io.Writer, extraOpts ...prober.Option) int {
	ghClient := prober.NewDefaultGitHubIssueClient(cfg.GitHubToken)
	return runWithProberAndClient(ctx, cfg, ghClient, stdout, stderr, extraOpts...)
}

// runWithProberAndClient constructs a Prober from Config and an explicit GitHubIssueClient.
func runWithProberAndClient(ctx context.Context, cfg *Config, ghClient prober.GitHubIssueClient, stdout, stderr io.Writer, extraOpts ...prober.Option) int {
	opts := []prober.Option{
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
	}
	if ghClient != nil {
		opts = append(opts, prober.WithGitHubClient(ghClient))
	}
	opts = append(opts, extraOpts...)

	p := prober.NewProber(opts...)
	return run(ctx, cfg, p, p.GitHubClient(), cfg.Repo, stdout, stderr)
}

func main() {
	cfg, err := parseConfig(os.Args[1:], os.Getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error parsing configuration: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	exitCode := runWithProber(ctx, cfg, os.Stdout, os.Stderr)
	os.Exit(exitCode)
}
