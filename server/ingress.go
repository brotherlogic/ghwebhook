package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
)

type githubPayload struct {
	Action      string            `json:"action"`
	Number      int32             `json:"number"`
	PullRequest *githubPR         `json:"pull_request"`
	Issue       *githubIssue      `json:"issue"`
	Repository  githubRepo        `json:"repository"`
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
	if r.URL.Path != "/webhook" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	signature := r.Header.Get("X-Hub-Signature-256")
	if !s.validateSignature(payload, signature) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var ghPayload githubPayload
	if err := json.Unmarshal(payload, &ghPayload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Mapping logic (to be expanded in Issue #4, but we can verify it here)
	eventType := r.Header.Get("X-GitHub-Event")
	_ = s.mapToProto(ghPayload, eventType)

	w.WriteHeader(http.StatusOK)
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
			EventType:     eventType,
			ReceivedAtMs: time.Now().UnixMilli(),
		},
	}

	switch eventType {
	case "pull_request":
		if gh.PullRequest != nil {
			event.Payload = &pb.WebhookEvent_PullRequest{
				PullRequest: &pb.PullRequestEvent{
					Action: gh.Action,
					Number: gh.Number,
					Title:  gh.PullRequest.Title,
					Body:   gh.PullRequest.Body,
					User:   &pb.User{Login: gh.PullRequest.User.Login},
					Repository: &pb.Repository{FullName: gh.Repository.FullName},
				},
			}
		}
	case "issue":
		if gh.Issue != nil {
			event.Payload = &pb.WebhookEvent_Issue{
				Issue: &pb.IssueEvent{
					Action: gh.Action,
					Number: gh.Number,
					Title:  gh.Issue.Title,
					Body:   gh.Issue.Body,
					User:   &pb.User{Login: gh.Issue.User.Login},
					Repository: &pb.Repository{FullName: gh.Repository.FullName},
				},
			}
		}
	}

	return event
}
