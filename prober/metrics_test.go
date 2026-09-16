package prober

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordResult(t *testing.T) {
	repo := "brotherlogic/test-repo"

	initialSuccess := testutil.ToFloat64(ProberRunsTotal.WithLabelValues(repo, "success"))
	initialSoft := testutil.ToFloat64(ProberRunsTotal.WithLabelValues(repo, "soft_failure"))
	initialHard := testutil.ToFloat64(ProberRunsTotal.WithLabelValues(repo, "hard_failure"))

	// Record StatusSuccess
	RecordResult(repo, Result{
		Status:   StatusSuccess,
		Duration: 1200 * time.Millisecond,
	})

	if val := testutil.ToFloat64(ProberRunsTotal.WithLabelValues(repo, "success")); val != initialSuccess+1 {
		t.Errorf("Expected success count %f, got %f", initialSuccess+1, val)
	}

	// Record StatusSoftFailure
	RecordResult(repo, Result{
		Status:   StatusSoftFailure,
		Duration: 500 * time.Millisecond,
	})

	if val := testutil.ToFloat64(ProberRunsTotal.WithLabelValues(repo, "soft_failure")); val != initialSoft+1 {
		t.Errorf("Expected soft_failure count %f, got %f", initialSoft+1, val)
	}

	// Record StatusHardFailure
	RecordResult(repo, Result{
		Status:   StatusHardFailure,
		Duration: 3500 * time.Millisecond,
	})

	if val := testutil.ToFloat64(ProberRunsTotal.WithLabelValues(repo, "hard_failure")); val != initialHard+1 {
		t.Errorf("Expected hard_failure count %f, got %f", initialHard+1, val)
	}
}

func TestRecordResult_Duration(t *testing.T) {
	repo := "brotherlogic/test-duration-repo"
	duration := 2500 * time.Millisecond

	RecordResult(repo, Result{
		Status:   StatusSuccess,
		Duration: duration,
	})

	// Verify last duration gauge
	lastDuration := testutil.ToFloat64(ProberLastDurationSeconds.WithLabelValues(repo))
	if lastDuration != duration.Seconds() {
		t.Errorf("Expected last duration gauge %f, got %f", duration.Seconds(), lastDuration)
	}

	// Verify duration histogram has observed the event
	histCount := testutil.CollectAndCount(ProberDurationSeconds)
	if histCount == 0 {
		t.Errorf("Expected ProberDurationSeconds to contain metric collectors, got 0")
	}
}

func getFreePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on free port: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

func TestServeMetricsUntilScraped_Success(t *testing.T) {
	repo := "brotherlogic/test-serve-success"
	RecordResult(repo, Result{
		Status:   StatusSuccess,
		Duration: 1500 * time.Millisecond,
	})

	addr := getFreePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeMetricsUntilScraped(ctx, addr, 5*time.Second)
	}()

	// Poll until server is ready and responds
	metricsURL := fmt.Sprintf("http://%s/metrics", addr)
	var resp *http.Response
	var getErr error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, getErr = http.Get(metricsURL)
		if getErr == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if getErr != nil {
		t.Fatalf("Failed to GET /metrics: %v", getErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 OK from /metrics, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if !strings.Contains(string(body), "ghwebhook_prober_runs_total") {
		t.Errorf("Expected metrics response to contain ghwebhook_prober_runs_total, got:\n%s", string(body))
	}
	if !strings.Contains(string(body), repo) {
		t.Errorf("Expected metrics response to contain repo %s, got:\n%s", repo, string(body))
	}

	// Verify ServeMetricsUntilScraped terminates promptly after being scraped
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeMetricsUntilScraped returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeMetricsUntilScraped did not terminate promptly after being scraped")
	}
}

func TestServeMetricsUntilScraped_Timeout(t *testing.T) {
	addr := getFreePort(t)
	ctx := context.Background()

	start := time.Now()
	timeout := 100 * time.Millisecond
	err := ServeMetricsUntilScraped(ctx, addr, timeout)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Expected nil error on timeout, got %v", err)
	}
	if elapsed < timeout {
		t.Errorf("ServeMetricsUntilScraped returned in %v, expected at least %v", elapsed, timeout)
	}
}

func TestServeMetricsUntilScraped_ContextCanceled(t *testing.T) {
	addr := getFreePort(t)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeMetricsUntilScraped(ctx, addr, 5*time.Second)
	}()

	// Allow server to start listening
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Expected nil error on context cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeMetricsUntilScraped did not terminate on context cancellation")
	}
}
