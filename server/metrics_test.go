package server

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricsRegistered(t *testing.T) {
	// Initialize label values to trigger registration in gatherer and verify label count
	IncomingEventsTotal.WithLabelValues("pull_request", "200")
	OutgoingEventsTotal.WithLabelValues("pull_request", "http://localhost:8080", "success")
	RegistrationsTotal.WithLabelValues("brotherlogic/ghwebhook", "http://localhost:8080")
	RegistrationStrikesTotal.WithLabelValues("brotherlogic/ghwebhook", "http://localhost:8080")
	RegistrationRemovalsTotal.WithLabelValues("brotherlogic/ghwebhook", "http://localhost:8080", "max_strikes")

	metricNames := []string{
		"ghwebhook_incoming_events_total",
		"ghwebhook_outgoing_events_total",
		"ghwebhook_registrations_total",
		"ghwebhook_registration_strikes_total",
		"ghwebhook_registration_removals_total",
	}

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	found := make(map[string]bool)
	for _, mf := range mfs {
		found[mf.GetName()] = true
	}

	for _, name := range metricNames {
		if !found[name] {
			var gathered []string
			for k := range found {
				gathered = append(gathered, k)
			}
			t.Errorf("metric %s not found in default registry. Gathered: %v", name, gathered)
		}
	}
}
