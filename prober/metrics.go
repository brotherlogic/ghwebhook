package prober

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// ProberRunsTotal tracks the total count of prober execution runs.
	ProberRunsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ghwebhook_prober_runs_total",
			Help: "Total count of prober execution runs.",
		},
		[]string{"repo", "status"},
	)

	// ProberDurationSeconds tracks the duration of prober execution runs in seconds.
	ProberDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ghwebhook_prober_duration_seconds",
			Help:    "Duration of prober execution runs in seconds.",
			Buckets: []float64{0.5, 1.0, 2.5, 5.0, 10.0, 15.0, 30.0, 45.0, 60.0, 90.0, 120.0},
		},
		[]string{"repo"},
	)

	// ProberLastDurationSeconds tracks the duration of the most recent prober execution run in seconds.
	ProberLastDurationSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ghwebhook_prober_last_duration_seconds",
			Help: "Duration of the most recent prober execution run in seconds.",
		},
		[]string{"repo"},
	)
)

// RecordResult records Prometheus metrics for a completed prober execution result.
func RecordResult(repo string, res Result) {
	var statusLabel string
	switch res.Status {
	case StatusSuccess:
		statusLabel = "success"
	case StatusSoftFailure:
		statusLabel = "soft_failure"
	case StatusHardFailure:
		statusLabel = "hard_failure"
	default:
		statusLabel = "unknown"
	}

	ProberRunsTotal.WithLabelValues(repo, statusLabel).Inc()
	ProberDurationSeconds.WithLabelValues(repo).Observe(res.Duration.Seconds())
	ProberLastDurationSeconds.WithLabelValues(repo).Set(res.Duration.Seconds())
}

// ServeMetricsUntilScraped binds an HTTP server on addr serving Prometheus metrics
// on /metrics until a successful scrape occurs, timeout expires, or ctx is canceled.
func ServeMetricsUntilScraped(ctx context.Context, addr string, timeout time.Duration) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	scrapedCh := make(chan struct{})
	var once sync.Once

	promHandler := promhttp.Handler()
	scrapeDetector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		promHandler.ServeHTTP(w, r)
		once.Do(func() {
			close(scrapedCh)
		})
	})

	mux := http.NewServeMux()
	mux.Handle("/metrics", scrapeDetector)

	server := &http.Server{
		Handler: mux,
	}

	serverErrCh := make(chan error, 1)
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErrCh <- serveErr
		}
		close(serverErrCh)
	}()

	var timer *time.Timer
	var timerCh <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timerCh = timer.C
	}

	select {
	case err := <-serverErrCh:
		return err
	case <-scrapedCh:
	case <-timerCh:
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
