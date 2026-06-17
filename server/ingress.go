package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	pstore_pb "github.com/brotherlogic/pstore/proto"
)

type githubPayload struct {
	Action      string       `json:"action"`
	Number      int32        `json:"number"`
	PullRequest *githubPR    `json:"pull_request"`
	Issue       *githubIssue `json:"issue"`
	Repository  githubRepo   `json:"repository"`
}

type githubPR struct {
	Title string     `json:"title"`
	Body  string     `json:"body"`
	User  githubUser `json:"user"`
}

type githubIssue struct {
	Title string     `json:"title"`
	Body  string     `json:"body"`
	User  githubUser `json:"user"`
}

type githubUser struct {
	Login string `json:"login"`
}

type githubRepo struct {
	FullName string `json:"full_name"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
		return
	}

	if r.URL.Path != "/webhook" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	metricEventType := eventType
	if eventType != "pull_request" && eventType != "issue" {
		log.Printf("Received event type other than pull_request or issue: %q", eventType)
		metricEventType = "unknown"
	}

	signature := r.Header.Get("X-Hub-Signature-256")
	if !s.validateSignature(payload, signature) {
		IncomingEventsTotal.WithLabelValues(metricEventType, "401").Inc()
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var ghPayload githubPayload
	if err := json.Unmarshal(payload, &ghPayload); err != nil {
		IncomingEventsTotal.WithLabelValues(metricEventType, "400").Inc()
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	event := s.mapToProto(ghPayload, eventType)

	if event.Payload != nil {
		s.routeEvent(r.Context(), event, ghPayload.Repository.FullName)
	}

	IncomingEventsTotal.WithLabelValues(metricEventType, "200").Inc()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) routeEvent(ctx context.Context, event *pb.WebhookEvent, repo string) {
	regs, err := s.getRegistrations(ctx, repo)
	if err != nil {
		log.Printf("Failed to get registrations for %s: %v", repo, err)
		return
	}

	var wg sync.WaitGroup
	for _, reg := range regs {
		wg.Add(1)
		go func(address string) {
			defer wg.Done()
			err := s.deliverWithRetry(ctx, event, address)
			key := fmt.Sprintf("ghwebhook/reg/%s/%s", repo, address)

			outgoingMetricEventType := event.Header.EventType
			if outgoingMetricEventType != "pull_request" && outgoingMetricEventType != "issue" {
				outgoingMetricEventType = "unknown"
			}

			if err != nil {
				log.Printf("Failed to deliver webhook to %s after retries: %v", address, err)
				OutgoingEventsTotal.WithLabelValues(outgoingMetricEventType, address, "failure").Inc()

				s.strikeLock.Lock()
				s.strikes[key]++
				count := s.strikes[key]
				s.strikeLock.Unlock()

				// Track strike metrics
				RegistrationStrikesTotal.WithLabelValues(repo, address).Inc()

				if count >= 3 {
					log.Printf("Service %s reached 3 strikes, removing registration", address)
					_, deleteErr := s.pstore.Delete(ctx, &pstore_pb.DeleteRequest{Key: key})
					if deleteErr != nil {
						log.Printf("Failed to delete registration for %s: %v", address, deleteErr)
					}
					// Track removal and registration count metrics
					RegistrationRemovalsTotal.WithLabelValues(repo, address, "max_strikes").Inc()
					RegistrationsTotal.WithLabelValues(repo, address).Set(0)
				}
			} else {
				OutgoingEventsTotal.WithLabelValues(outgoingMetricEventType, address, "success").Inc()

				// Reset strikes on success
				s.strikeLock.Lock()
				delete(s.strikes, key)
				s.strikeLock.Unlock()
			}
		}(reg.ServiceAddress)
	}
	wg.Wait()
}

func (s *Server) validateSignature(payload []byte, signature string) bool {
	secret := os.Getenv("GH_WEBHOOK_SECRET")
	if secret == "" {
		return false
	}

	if signature == "" {
		return false
	}

	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	expectedSignature := "sha256=" + hex.EncodeToString(h.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}

func (s *Server) mapToProto(gh githubPayload, eventType string) *pb.WebhookEvent {
	event := &pb.WebhookEvent{
		Header: &pb.EventHeader{
			EventType:    eventType,
			ReceivedAtMs: time.Now().UnixMilli(),
		},
	}

	switch eventType {
	case "pull_request":
		if gh.PullRequest != nil {
			event.Payload = &pb.WebhookEvent_PullRequest{
				PullRequest: &pb.PullRequestEvent{
					Action:     gh.Action,
					Number:     gh.Number,
					Title:      gh.PullRequest.Title,
					Body:       gh.PullRequest.Body,
					User:       &pb.User{Login: gh.PullRequest.User.Login},
					Repository: &pb.Repository{FullName: gh.Repository.FullName},
				},
			}
		}
	case "issue":
		if gh.Issue != nil {
			event.Payload = &pb.WebhookEvent_Issue{
				Issue: &pb.IssueEvent{
					Action:     gh.Action,
					Number:     gh.Number,
					Title:      gh.Issue.Title,
					Body:       gh.Issue.Body,
					User:       &pb.User{Login: gh.Issue.User.Login},
					Repository: &pb.Repository{FullName: gh.Repository.FullName},
				},
			}
		}
	}

	return event
}
