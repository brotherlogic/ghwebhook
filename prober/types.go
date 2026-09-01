package prober

import (
	"context"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"github.com/google/go-github/v69/github"
	"google.golang.org/grpc"
)

// ResultStatus represents the outcome status of the prober execution.
type ResultStatus int

const (
	// StatusSuccess indicates the webhook was received and validated within timeout.
	StatusSuccess ResultStatus = 0
	// StatusHardFailure indicates timeout exceeded or payload validation mismatch.
	StatusHardFailure ResultStatus = 1
	// StatusSoftFailure indicates upstream GitHub API rate limit / 5xx / network error.
	StatusSoftFailure ResultStatus = 2
)

func (s ResultStatus) String() string {
	switch s {
	case StatusSuccess:
		return "SUCCESS"
	case StatusHardFailure:
		return "HARD_FAILURE"
	case StatusSoftFailure:
		return "SOFT_FAILURE"
	default:
		return "UNKNOWN"
	}
}

// Result captures the full execution result of a Prober run.
type Result struct {
	Status      ResultStatus
	Duration    time.Duration
	IssueNumber int
	Action      string
	Message     string
	Err         error
}

// Defaults for prober configuration options.
const (
	DefaultRepo          = "brotherlogic/ghwebhook"
	DefaultTargetTitle   = "PROBER TEST"
	DefaultGHWebhookAddr = "localhost:50051"
	DefaultListenAddr    = ":50052"
	DefaultServiceAddr   = "127.0.0.1:50052"
	DefaultTimeout       = 60 * time.Second
)

// GitHubIssueClient abstracts GitHub issue operations required by the prober.
type GitHubIssueClient interface {
	SearchIssues(ctx context.Context, owner, repo, query string) ([]*github.Issue, error)
	CreateIssue(ctx context.Context, owner, repo string, req *github.IssueRequest) (*github.Issue, error)
	EditIssue(ctx context.Context, owner, repo string, number int, req *github.IssueRequest) (*github.Issue, error)
}

// Prober manages the end-to-end validation lifecycle.
type Prober struct {
	pb.UnimplementedWebhookHandlerServer

	ghClient      GitHubIssueClient
	regClient     pb.RegistrationServiceClient
	repoFullName  string
	targetTitle   string
	ghwebhookAddr string
	listenAddr    string
	serviceAddr   string
	timeout       time.Duration
	eventCh       chan *pb.WebhookEvent
	grpcServer    *grpc.Server
}

// Option configures a Prober instance.
type Option func(*Prober)

// WithRepo configures the target repository full name (e.g. "brotherlogic/ghwebhook").
func WithRepo(repo string) Option {
	return func(p *Prober) {
		p.repoFullName = repo
	}
}

// WithTargetTitle configures the title of the test issue (e.g. "PROBER TEST").
func WithTargetTitle(title string) Option {
	return func(p *Prober) {
		p.targetTitle = title
	}
}

// WithGHWebhookAddr configures the address of the ghwebhook registration gRPC service.
func WithGHWebhookAddr(addr string) Option {
	return func(p *Prober) {
		p.ghwebhookAddr = addr
	}
}

// WithListenAddr configures the local address where the prober gRPC WebhookHandler listens.
func WithListenAddr(addr string) Option {
	return func(p *Prober) {
		p.listenAddr = addr
	}
}

// WithServiceAddr configures the service address advertised to ghwebhook during registration.
func WithServiceAddr(addr string) Option {
	return func(p *Prober) {
		p.serviceAddr = addr
	}
}

// WithTimeout configures the maximum duration to wait for webhook delivery.
func WithTimeout(timeout time.Duration) Option {
	return func(p *Prober) {
		p.timeout = timeout
	}
}

// WithGitHubClient configures the GitHub API client interface.
func WithGitHubClient(client GitHubIssueClient) Option {
	return func(p *Prober) {
		p.ghClient = client
	}
}

// WithRegistrationClient configures the ghwebhook RegistrationService gRPC client.
func WithRegistrationClient(client pb.RegistrationServiceClient) Option {
	return func(p *Prober) {
		p.regClient = client
	}
}

// NewProber creates a new Prober instance initialized with defaults and overridden by options.
func NewProber(opts ...Option) *Prober {
	p := &Prober{
		repoFullName:  DefaultRepo,
		targetTitle:   DefaultTargetTitle,
		ghwebhookAddr: DefaultGHWebhookAddr,
		listenAddr:    DefaultListenAddr,
		serviceAddr:   DefaultServiceAddr,
		timeout:       DefaultTimeout,
		eventCh:       make(chan *pb.WebhookEvent, 10),
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}
