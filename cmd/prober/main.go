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

// Config encapsulates configuration parameters for the prober CLI.
type Config struct {
	Repo          string
	GHWebhookAddr string
	ListenAddr    string
	ServiceAddr   string
	Timeout       time.Duration
	GitHubToken   string
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

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	return &Config{
		Repo:          repo,
		GHWebhookAddr: ghwebhookAddr,
		ListenAddr:    listenAddr,
		ServiceAddr:   serviceAddr,
		Timeout:       timeout,
		GitHubToken:   token,
	}, nil
}

// run executes the prober instance, emits structured logs, and returns the exit code.
func run(ctx context.Context, p *prober.Prober, stdout, stderr io.Writer) int {
	result, _ := p.Run(ctx)

	handler := slog.NewJSONHandler(stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)

	var errStr string
	if result.Err != nil {
		errStr = result.Err.Error()
	}

	logger.Info("prober execution completed",
		slog.String("status", result.Status.String()),
		slog.Int("status_code", int(result.Status)),
		slog.String("action", result.Action),
		slog.Int("issue_number", result.IssueNumber),
		slog.Duration("duration", result.Duration),
		slog.String("message", result.Message),
		slog.String("error", errStr),
	)

	return int(result.Status)
}

// runWithProber constructs a Prober from Config and executes it.
func runWithProber(ctx context.Context, cfg *Config, stdout, stderr io.Writer, extraOpts ...prober.Option) int {
	ghClient := prober.NewDefaultGitHubIssueClient(cfg.GitHubToken)

	opts := []prober.Option{
		prober.WithRepo(cfg.Repo),
		prober.WithGHWebhookAddr(cfg.GHWebhookAddr),
		prober.WithListenAddr(cfg.ListenAddr),
		prober.WithServiceAddr(cfg.ServiceAddr),
		prober.WithTimeout(cfg.Timeout),
		prober.WithGitHubClient(ghClient),
	}
	opts = append(opts, extraOpts...)

	p := prober.NewProber(opts...)
	return run(ctx, p, stdout, stderr)
}

func main() {
	cfg, err := parseConfig(os.Args[1:], os.Getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error parsing configuration: %v\n", err)
		os.Exit(int(prober.StatusHardFailure))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	exitCode := runWithProber(ctx, cfg, os.Stdout, os.Stderr)
	os.Exit(exitCode)
}
