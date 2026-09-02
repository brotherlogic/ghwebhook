package prober

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"github.com/google/go-github/v69/github"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// StartGRPCServer initializes the TCP listener and starts serving the WebhookHandler gRPC service.
// It also listens on ctx.Done() to execute a graceful shutdown when the context is cancelled.
func (p *Prober) StartGRPCServer(ctx context.Context) error {
	p.mu.Lock()
	if p.grpcServer != nil {
		p.mu.Unlock()
		return errors.New("gRPC server is already running")
	}

	lis, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		p.mu.Unlock()
		return err
	}

	p.listener = lis
	p.listenAddr = lis.Addr().String()

	grpcServer := grpc.NewServer()
	pb.RegisterWebhookHandlerServer(grpcServer, p)
	p.grpcServer = grpcServer
	p.mu.Unlock()

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	go func() {
		<-ctx.Done()
		p.StopGRPCServer()
	}()

	return nil
}

// StopGRPCServer gracefully stops the running gRPC server and closes the underlying listener.
func (p *Prober) StopGRPCServer() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.grpcServer != nil {
		p.grpcServer.GracefulStop()
		p.grpcServer = nil
	}
	if p.listener != nil {
		_ = p.listener.Close()
		p.listener = nil
	}
}

// ReceiveWebhook handles incoming WebhookEvent RPCs from ghwebhook.
// It parses the payload, validates it against the configured target repository, title, issue number,
// and action, and dispatches matching events onto the internal eventCh channel.
func (p *Prober) ReceiveWebhook(ctx context.Context, req *pb.WebhookEvent) (*pb.WebhookResponse, error) {
	if req == nil {
		return &pb.WebhookResponse{
			Success: false,
			Message: "empty request payload",
		}, nil
	}

	issue := req.GetIssue()
	if issue == nil {
		return &pb.WebhookResponse{
			Success: true,
			Message: "ignored: non-issue event",
		}, nil
	}

	if p.repoFullName != "" && issue.GetRepository().GetFullName() != p.repoFullName {
		return &pb.WebhookResponse{
			Success: true,
			Message: "ignored: repository mismatch",
		}, nil
	}

	if p.targetTitle != "" && issue.GetTitle() != p.targetTitle {
		return &pb.WebhookResponse{
			Success: true,
			Message: "ignored: title mismatch",
		}, nil
	}

	p.mu.Lock()
	expectedNumber := p.targetIssueNumber
	expectedAction := p.targetAction
	p.mu.Unlock()

	if expectedNumber > 0 && issue.GetNumber() != int32(expectedNumber) {
		return &pb.WebhookResponse{
			Success: true,
			Message: "ignored: issue number mismatch",
		}, nil
	}

	if expectedAction != "" && issue.GetAction() != expectedAction {
		return &pb.WebhookResponse{
			Success: true,
			Message: "ignored: action mismatch",
		}, nil
	}

	select {
	case p.eventCh <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		// Channel buffer full
	}

	return &pb.WebhookResponse{
		Success: true,
		Message: "event received and matched",
	}, nil
}

// Run executes the complete prober validation lifecycle:
// 1. Starts the local gRPC WebhookHandler server.
// 2. Registers the prober with ghwebhook.
// 3. Mutates an issue titled "PROBER TEST" on GitHub (create/reopen/close).
// 4. Waits for the corresponding webhook event to be delivered and validated within timeout.
// 5. Cleans up by unregistering from ghwebhook, closing open test issues, and stopping the server.
func (p *Prober) Run(ctx context.Context) (Result, error) {
	startTime := time.Now()

	// Parse repo owner/name
	parts := strings.Split(p.repoFullName, "/")
	if len(parts) != 2 {
		err := fmt.Errorf("invalid repository full name %q: expected owner/repo", p.repoFullName)
		return Result{
			Status:   StatusHardFailure,
			Duration: time.Since(startTime),
			Message:  err.Error(),
			Err:      err,
		}, err
	}
	owner, repo := parts[0], parts[1]

	// Start local gRPC server
	if err := p.StartGRPCServer(ctx); err != nil {
		return Result{
			Status:   StatusHardFailure,
			Duration: time.Since(startTime),
			Message:  fmt.Sprintf("failed to start prober gRPC server: %v", err),
			Err:      err,
		}, err
	}

	// Determine service address advertised to ghwebhook
	serviceAddr := p.serviceAddr
	if serviceAddr == "" {
		serviceAddr = p.ListenAddr()
	} else if _, port, err := net.SplitHostPort(serviceAddr); err == nil && port == "0" {
		_, listenPort, err := net.SplitHostPort(p.ListenAddr())
		if err == nil {
			host, _, _ := net.SplitHostPort(serviceAddr)
			serviceAddr = net.JoinHostPort(host, listenPort)
		}
	}

	// Establish registration client
	regClient := p.regClient
	var grpcConn *grpc.ClientConn
	if regClient == nil {
		conn, err := grpc.NewClient(
			p.ghwebhookAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			p.StopGRPCServer()
			return Result{
				Status:   StatusHardFailure,
				Duration: time.Since(startTime),
				Message:  fmt.Sprintf("failed to connect to ghwebhook registration service: %v", err),
				Err:      err,
			}, err
		}
		grpcConn = conn
		regClient = pb.NewRegistrationServiceClient(conn)
	}

	var issueLeftOpen bool
	var targetIssueNumber int
	var targetAction string

	// Deferred cleanup ensures unregister, issue closure, and server teardown happen on all exit paths
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		if regClient != nil {
			_, _ = regClient.Unregister(cleanupCtx, &pb.UnregisterRequest{
				RepoFullName:   p.repoFullName,
				ServiceAddress: serviceAddr,
			})
		}

		if grpcConn != nil {
			_ = grpcConn.Close()
		}

		if issueLeftOpen && targetIssueNumber > 0 && p.ghClient != nil {
			_, _ = p.ghClient.EditIssue(cleanupCtx, owner, repo, targetIssueNumber, &github.IssueRequest{
				State: github.Ptr("closed"),
			})
		}

		p.StopGRPCServer()
	}()

	// Register with ghwebhook proxy
	regResp, err := regClient.Register(ctx, &pb.RegistrationRequest{
		RepoFullName:   p.repoFullName,
		ServiceAddress: serviceAddr,
	})
	if err != nil || (regResp != nil && !regResp.GetSuccess()) {
		if err == nil {
			err = errors.New("registration rejected by ghwebhook")
		}
		return Result{
			Status:   StatusHardFailure,
			Duration: time.Since(startTime),
			Message:  fmt.Sprintf("failed to register with ghwebhook: %v", err),
			Err:      err,
		}, err
	}

	// Ensure GitHubIssueClient is initialized
	if p.ghClient == nil {
		p.ghClient = NewDefaultGitHubIssueClient("")
	}

	// Search for existing test issue
	issues, err := p.ghClient.SearchIssues(ctx, owner, repo, p.targetTitle)
	if err != nil {
		return Result{
			Status:   StatusSoftFailure,
			Duration: time.Since(startTime),
			Message:  fmt.Sprintf("failed to search issues on GitHub: %v", err),
			Err:      err,
		}, err
	}

	var foundIssue *github.Issue
	for _, iss := range issues {
		if iss.GetTitle() == p.targetTitle {
			foundIssue = iss
			break
		}
	}

	// Mutate issue state based on existence and current status
	if foundIssue == nil {
		targetAction = "opened"
		created, err := p.ghClient.CreateIssue(ctx, owner, repo, &github.IssueRequest{
			Title: github.Ptr(p.targetTitle),
			Body:  github.Ptr("Automated prober validation test issue"),
		})
		if err != nil {
			return Result{
				Status:   StatusSoftFailure,
				Duration: time.Since(startTime),
				Message:  fmt.Sprintf("failed to create test issue on GitHub: %v", err),
				Err:      err,
			}, err
		}
		targetIssueNumber = created.GetNumber()
		issueLeftOpen = true
	} else if foundIssue.GetState() == "closed" {
		targetAction = "reopened"
		targetIssueNumber = foundIssue.GetNumber()
		_, err := p.ghClient.EditIssue(ctx, owner, repo, targetIssueNumber, &github.IssueRequest{
			State: github.Ptr("open"),
		})
		if err != nil {
			return Result{
				Status:   StatusSoftFailure,
				Duration: time.Since(startTime),
				Message:  fmt.Sprintf("failed to reopen test issue on GitHub: %v", err),
				Err:      err,
			}, err
		}
		issueLeftOpen = true
	} else {
		targetAction = "closed"
		targetIssueNumber = foundIssue.GetNumber()
		_, err := p.ghClient.EditIssue(ctx, owner, repo, targetIssueNumber, &github.IssueRequest{
			State: github.Ptr("closed"),
		})
		if err != nil {
			return Result{
				Status:   StatusSoftFailure,
				Duration: time.Since(startTime),
				Message:  fmt.Sprintf("failed to close test issue on GitHub: %v", err),
				Err:      err,
			}, err
		}
		issueLeftOpen = false
	}

	p.SetTargetIssueNumber(targetIssueNumber)
	p.SetTargetAction(targetAction)

	// Wait for matching webhook delivery or timeout
	timer := time.NewTimer(p.timeout)
	defer timer.Stop()

	select {
	case <-p.eventCh:
		return Result{
			Status:      StatusSuccess,
			Duration:    time.Since(startTime),
			IssueNumber: targetIssueNumber,
			Action:      targetAction,
			Message:     fmt.Sprintf("successfully received and validated webhook for issue #%d (%s)", targetIssueNumber, targetAction),
		}, nil
	case <-timer.C:
		return Result{
			Status:      StatusHardFailure,
			Duration:    time.Since(startTime),
			IssueNumber: targetIssueNumber,
			Action:      targetAction,
			Message:     fmt.Sprintf("timed out waiting for webhook event after %v", p.timeout),
			Err:         errors.New("timeout waiting for webhook event"),
		}, nil
	case <-ctx.Done():
		return Result{
			Status:      StatusHardFailure,
			Duration:    time.Since(startTime),
			IssueNumber: targetIssueNumber,
			Action:      targetAction,
			Message:     "context cancelled before webhook received",
			Err:         ctx.Err(),
		}, ctx.Err()
	}
}
