package prober

import (
	"net"
	"sync"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
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

// RootCause classifies the underlying reason for a prober webhook delivery failure.
type RootCause string

const (
	// RootCauseWebhookMissing indicates no active webhook listening for issues was found on the repository.
	RootCauseWebhookMissing RootCause = "Webhook Missing"
	// RootCauseDeliveryFailed indicates GitHub attempted delivery but received a non-2xx HTTP status or network failure.
	RootCauseDeliveryFailed RootCause = "GitHub Delivery Failed"
	// RootCauseLostInRouting indicates GitHub successfully delivered the webhook (HTTP 2xx) but it was not received or matched by the prober handler.
	RootCauseLostInRouting RootCause = "Delivered by GitHub but Lost in Routing"
	// RootCauseNoDeliveryAttempted indicates active webhooks exist but GitHub made no delivery attempt for the prober event.
	RootCauseNoDeliveryAttempted RootCause = "No Delivery Attempted"
	// RootCauseInspectionUnavailable indicates the prober could not inspect webhook deliveries (e.g. insufficient token permissions or API errors).
	RootCauseInspectionUnavailable RootCause = "Inspection Unavailable"
)

// HookDeliverySummary summarizes the diagnostic details of a single GitHub webhook delivery attempt.
type HookDeliverySummary struct {
	HookID      int64
	DeliveryID  int64
	GUID        string
	DeliveredAt time.Time
	StatusCode  int
	Status      string
	Duration    float64
	Event       string
	Action      string
}

// InspectionDiagnostics encapsulates the root cause and delivery inspection findings for a failed prober run.
type InspectionDiagnostics struct {
	RootCause          RootCause
	RootCauseDetail    string
	ActiveHooksCount   int
	MatchingDeliveries []HookDeliverySummary
	ErrorMessage       string
}

// Result captures the full execution result of a Prober run.
type Result struct {
	Status      ResultStatus
	Duration    time.Duration
	IssueNumber int
	Action      string
	Message     string
	Err         error
	Diagnostics *InspectionDiagnostics
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

// Prober manages the end-to-end validation lifecycle.
type Prober struct {
	pb.UnimplementedWebhookHandlerServer

	ghClient          GitHubIssueClient
	hookClient        GitHubHookClient
	regClient         pb.RegistrationServiceClient
	repoFullName      string
	targetTitle       string
	targetIssueNumber int
	targetAction      string
	ghwebhookAddr     string
	listenAddr        string
	serviceAddr       string
	ingressURL        string
	timeout           time.Duration
	eventCh           chan *pb.WebhookEvent
	grpcServer        *grpc.Server
	listener          net.Listener
	mu                sync.Mutex
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

// WithTargetIssueNumber configures the expected issue number to match against incoming webhooks.
func WithTargetIssueNumber(number int) Option {
	return func(p *Prober) {
		p.targetIssueNumber = number
	}
}

// WithTargetAction configures the expected issue action to match against incoming webhooks.
func WithTargetAction(action string) Option {
	return func(p *Prober) {
		p.targetAction = action
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

// WithIngressURL configures the ingress URL for webhook deliveries.
func WithIngressURL(url string) Option {
	return func(p *Prober) {
		p.ingressURL = url
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

// WithHookClient configures the GitHubHookClient interface.
func WithHookClient(client GitHubHookClient) Option {
	return func(p *Prober) {
		p.hookClient = client
	}
}

// WithRegistrationClient configures the ghwebhook RegistrationService gRPC client.
func WithRegistrationClient(client pb.RegistrationServiceClient) Option {
	return func(p *Prober) {
		p.regClient = client
	}
}

// SetTargetIssueNumber dynamically sets the expected issue number on the Prober.
func (p *Prober) SetTargetIssueNumber(number int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targetIssueNumber = number
}

// SetTargetAction dynamically sets the expected issue action on the Prober.
func (p *Prober) SetTargetAction(action string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targetAction = action
}

// ListenAddr returns the configured or actively bound listener address.
func (p *Prober) ListenAddr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.listenAddr
}

// EventChannel returns a receive-only channel for matching webhook events.
func (p *Prober) EventChannel() <-chan *pb.WebhookEvent {
	return p.eventCh
}

// GitHubClient returns the configured GitHubIssueClient on the Prober.
func (p *Prober) GitHubClient() GitHubIssueClient {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ghClient
}

// HookClient returns the configured GitHubHookClient on the Prober.
func (p *Prober) HookClient() GitHubHookClient {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hookClient
}

// RepoFullName returns the configured repository full name on the Prober.
func (p *Prober) RepoFullName() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.repoFullName
}

// IngressURL returns the configured ingress URL on the Prober.
func (p *Prober) IngressURL() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ingressURL
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
