package prober

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestReceiveWebhook_NilRequest(t *testing.T) {
	p := NewProber()
	resp, err := p.ReceiveWebhook(context.Background(), nil)
	if err != nil {
		t.Fatalf("ReceiveWebhook with nil req returned error: %v", err)
	}
	if resp == nil || resp.Success {
		t.Errorf("ReceiveWebhook with nil req resp = %v, want success=false", resp)
	}
}

func TestReceiveWebhook_NonIssuePayload(t *testing.T) {
	p := NewProber(WithRepo("brotherlogic/ghwebhook"), WithTargetTitle("PROBER TEST"))

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "pull_request",
		},
		Payload: &pb.WebhookEvent_PullRequest{
			PullRequest: &pb.PullRequestEvent{
				Action: "opened",
				Number: 1,
				Title:  "PROBER TEST",
				Repository: &pb.Repository{
					FullName: "brotherlogic/ghwebhook",
				},
			},
		},
	}

	resp, err := p.ReceiveWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected success=true (handled/ignored), got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		t.Fatalf("expected no event on eventCh, got %v", ev)
	default:
		// OK
	}
}

func TestReceiveWebhook_RepoMismatch(t *testing.T) {
	p := NewProber(WithRepo("brotherlogic/ghwebhook"), WithTargetTitle("PROBER TEST"))

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "issues",
		},
		Payload: &pb.WebhookEvent_Issue{
			Issue: &pb.IssueEvent{
				Action: "opened",
				Number: 10,
				Title:  "PROBER TEST",
				Repository: &pb.Repository{
					FullName: "other/repo",
				},
			},
		},
	}

	resp, err := p.ReceiveWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected success=true, got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		t.Fatalf("expected no event dispatched on repo mismatch, got %v", ev)
	default:
		// OK
	}
}

func TestReceiveWebhook_TitleMismatch(t *testing.T) {
	p := NewProber(WithRepo("brotherlogic/ghwebhook"), WithTargetTitle("PROBER TEST"))

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "issues",
		},
		Payload: &pb.WebhookEvent_Issue{
			Issue: &pb.IssueEvent{
				Action: "opened",
				Number: 10,
				Title:  "SOME OTHER TITLE",
				Repository: &pb.Repository{
					FullName: "brotherlogic/ghwebhook",
				},
			},
		},
	}

	resp, err := p.ReceiveWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected success=true, got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		t.Fatalf("expected no event dispatched on title mismatch, got %v", ev)
	default:
		// OK
	}
}

func TestReceiveWebhook_IssueNumberMismatch(t *testing.T) {
	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithTargetIssueNumber(42),
	)

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "issues",
		},
		Payload: &pb.WebhookEvent_Issue{
			Issue: &pb.IssueEvent{
				Action: "opened",
				Number: 99,
				Title:  "PROBER TEST",
				Repository: &pb.Repository{
					FullName: "brotherlogic/ghwebhook",
				},
			},
		},
	}

	resp, err := p.ReceiveWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected success=true, got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		t.Fatalf("expected no event dispatched on number mismatch, got %v", ev)
	default:
		// OK
	}
}

func TestReceiveWebhook_ActionMismatch(t *testing.T) {
	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithTargetAction("closed"),
	)

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "issues",
		},
		Payload: &pb.WebhookEvent_Issue{
			Issue: &pb.IssueEvent{
				Action: "opened",
				Number: 10,
				Title:  "PROBER TEST",
				Repository: &pb.Repository{
					FullName: "brotherlogic/ghwebhook",
				},
			},
		},
	}

	resp, err := p.ReceiveWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected success=true, got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		t.Fatalf("expected no event dispatched on action mismatch, got %v", ev)
	default:
		// OK
	}
}

func TestReceiveWebhook_MatchingEvent_Dispatched(t *testing.T) {
	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithTargetIssueNumber(42),
		WithTargetAction("reopened"),
	)

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "issues",
		},
		Payload: &pb.WebhookEvent_Issue{
			Issue: &pb.IssueEvent{
				Action: "reopened",
				Number: 42,
				Title:  "PROBER TEST",
				Repository: &pb.Repository{
					FullName: "brotherlogic/ghwebhook",
				},
			},
		},
	}

	resp, err := p.ReceiveWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected success=true, got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		if ev.GetIssue().GetNumber() != 42 {
			t.Errorf("ev.Number = %d, want 42", ev.GetIssue().GetNumber())
		}
		if ev.GetIssue().GetAction() != "reopened" {
			t.Errorf("ev.Action = %q, want 'reopened'", ev.GetIssue().GetAction())
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for event on eventCh")
	}
}

func TestStartGRPCServer_LifecycleAndRPC(t *testing.T) {
	p := NewProber(
		WithRepo("brotherlogic/ghwebhook"),
		WithTargetTitle("PROBER TEST"),
		WithListenAddr("127.0.0.1:0"),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.StartGRPCServer(ctx); err != nil {
		t.Fatalf("StartGRPCServer failed: %v", err)
	}
	defer p.StopGRPCServer()

	// Wait for listener to be active
	addr := p.ListenAddr()
	if addr == "" || addr == "127.0.0.1:0" {
		t.Fatalf("ListenAddr was not updated with dynamic port: %s", addr)
	}

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial prober gRPC server: %v", err)
	}
	defer conn.Close()

	client := pb.NewWebhookHandlerClient(conn)

	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType: "issues",
		},
		Payload: &pb.WebhookEvent_Issue{
			Issue: &pb.IssueEvent{
				Action: "opened",
				Number: 100,
				Title:  "PROBER TEST",
				Repository: &pb.Repository{
					FullName: "brotherlogic/ghwebhook",
				},
			},
		},
	}

	resp, err := client.ReceiveWebhook(ctx, event)
	if err != nil {
		t.Fatalf("client.ReceiveWebhook error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected resp.Success = true, got %v", resp)
	}

	select {
	case ev := <-p.eventCh:
		if ev.GetIssue().GetNumber() != 100 {
			t.Errorf("ev.Number = %d, want 100", ev.GetIssue().GetNumber())
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for event on eventCh from gRPC call")
	}

	// Test graceful shutdown
	p.StopGRPCServer()

	// Verify server stopped
	time.Sleep(100 * time.Millisecond)
	_, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if dialErr == nil {
		t.Error("expected connection failure after StopGRPCServer, but connection succeeded")
	}
}

func TestStartGRPCServer_ContextCancellation(t *testing.T) {
	p := NewProber(
		WithListenAddr("127.0.0.1:0"),
	)

	ctx, cancel := context.WithCancel(context.Background())

	if err := p.StartGRPCServer(ctx); err != nil {
		t.Fatalf("StartGRPCServer failed: %v", err)
	}

	addr := p.ListenAddr()
	cancel()

	// Give time for goroutine watching ctx.Done() to execute GracefulStop
	time.Sleep(200 * time.Millisecond)

	_, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if dialErr == nil {
		t.Error("expected connection failure after context cancel, but connection succeeded")
	}
}

func TestStartGRPCServer_InvalidAddress(t *testing.T) {
	p := NewProber(
		WithListenAddr("invalid-hostname-12345.local:99999"),
	)

	err := p.StartGRPCServer(context.Background())
	if err == nil {
		p.StopGRPCServer()
		t.Fatal("expected error starting gRPC server on invalid address, got nil")
	}
}
