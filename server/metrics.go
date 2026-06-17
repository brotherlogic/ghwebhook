package server

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// IncomingEventsTotal tracks the total number of incoming GitHub webhook events.
	IncomingEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ghwebhook_incoming_events_total",
			Help: "Total number of incoming GitHub webhook events.",
		},
		[]string{"event_type", "status"},
	)

	// OutgoingEventsTotal tracks the total number of outgoing webhook dispatches.
	OutgoingEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ghwebhook_outgoing_events_total",
			Help: "Total number of outgoing webhook dispatches.",
		},
		[]string{"event_type", "destination", "status"},
	)

	// RegistrationsTotal tracks the active webhook registrations.
	RegistrationsTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ghwebhook_registrations_total",
			Help: "Total number of active webhook registrations.",
		},
		[]string{"repo", "destination"},
	)

	// RegistrationStrikesTotal tracks accumulated registration delivery strikes.
	RegistrationStrikesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ghwebhook_registration_strikes_total",
			Help: "Total number of accumulated registration delivery strikes.",
		},
		[]string{"repo", "destination"},
	)

	// RegistrationRemovalsTotal tracks registration removals.
	RegistrationRemovalsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ghwebhook_registration_removals_total",
			Help: "Total number of registration removals.",
		},
		[]string{"repo", "destination", "reason"},
	)
)
