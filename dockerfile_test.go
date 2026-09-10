package main

import (
	"os"
	"strings"
	"testing"
)

func TestDockerfile_BuildsAndCopiesProber(t *testing.T) {
	content, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("Failed to read Dockerfile: %v", err)
	}
	s := string(content)

	// Ensure prober is compiled from ./cmd/prober
	if !strings.Contains(s, "./cmd/prober") {
		t.Errorf("Dockerfile missing build step for ./cmd/prober")
	}

	// Ensure prober binary is copied into the runtime stage
	lines := strings.Split(s, "\n")
	hasProberCopy := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "COPY") && strings.Contains(trimmed, "prober") {
			hasProberCopy = true
			break
		}
	}
	if !hasProberCopy {
		t.Errorf("Dockerfile missing COPY step for prober binary")
	}
}
