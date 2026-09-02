package prober

import (
	"context"
	"errors"
	"net"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"google.golang.org/grpc"
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
