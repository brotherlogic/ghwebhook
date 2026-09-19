package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflowConfig struct {
	Name string `yaml:"name"`
	On   struct {
		Push struct {
			Branches []string `yaml:"branches"`
			Tags     []string `yaml:"tags"`
		} `yaml:"push"`
	} `yaml:"on"`
}

func TestDockerBuildWorkflow_TriggersOnlyOnTags(t *testing.T) {
	content, err := os.ReadFile(".github/workflows/docker-build.yml")
	if err != nil {
		t.Fatalf("Failed to read docker-build.yml: %v", err)
	}

	var wf workflowConfig
	if err := yaml.Unmarshal(content, &wf); err != nil {
		t.Fatalf("Failed to parse docker-build.yml: %v", err)
	}

	// Ensure no branch triggers exist (specifically 'main' or any branches)
	if len(wf.On.Push.Branches) > 0 {
		t.Errorf("Expected docker-build.yml to not trigger on branches, but got branches: %v", wf.On.Push.Branches)
	}

	// Ensure tag triggers are present
	if len(wf.On.Push.Tags) == 0 {
		t.Errorf("Expected docker-build.yml to have tag triggers configured, but got none")
	}

	hasTagPattern := false
	for _, tag := range wf.On.Push.Tags {
		if tag == "v*.*.*" || tag == "v*" {
			hasTagPattern = true
			break
		}
	}
	if !hasTagPattern {
		t.Errorf("Expected docker-build.yml to trigger on v* tags, but got tags: %v", wf.On.Push.Tags)
	}

	// Verify metadata action configuration includes semver and latest
	s := string(content)
	if !strings.Contains(s, "type=semver,pattern={{version}}") {
		t.Errorf("Expected docker-build.yml to contain SemVer metadata configuration")
	}
	if !strings.Contains(s, "type=raw,value=latest") {
		t.Errorf("Expected docker-build.yml to contain latest tag metadata configuration")
	}
}
