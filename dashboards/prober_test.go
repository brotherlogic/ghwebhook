package dashboards

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type Dashboard struct {
	Title      string     `json:"title"`
	UID        string     `json:"uid"`
	Timezone   string     `json:"timezone"`
	Templating Templating `json:"templating"`
	Panels     []Panel    `json:"panels"`
}

type Templating struct {
	List []TemplateVar `json:"list"`
}

type TemplateVar struct {
	Name       string      `json:"name"`
	Type       string      `json:"type"`
	Query      interface{} `json:"query"`
	Multi      bool        `json:"multi"`
	IncludeAll bool        `json:"includeAll"`
	AllValue   string      `json:"allValue"`
	Datasource interface{} `json:"datasource"`
}

type Panel struct {
	ID          int           `json:"id"`
	Title       string        `json:"title"`
	Type        string        `json:"type"`
	Datasource  interface{}   `json:"datasource"`
	Targets     []Target      `json:"targets"`
	FieldConfig FieldConfig   `json:"fieldConfig"`
	Panels      []Panel       `json:"panels,omitempty"` // For row panels
}

type Target struct {
	Expr         string      `json:"expr"`
	RefID        string      `json:"refId"`
	LegendFormat string      `json:"legendFormat"`
	Datasource   interface{} `json:"datasource"`
}

type FieldConfig struct {
	Defaults  FieldDefaults   `json:"defaults"`
	Overrides []FieldOverride `json:"overrides"`
}

type FieldDefaults struct {
	Thresholds Thresholds `json:"thresholds"`
	Mappings   []Mapping  `json:"mappings"`
}

type Thresholds struct {
	Mode  string          `json:"mode"`
	Steps []ThresholdStep `json:"steps"`
}

type ThresholdStep struct {
	Color string   `json:"color"`
	Value *float64 `json:"value"`
}

type FieldOverride struct {
	Matcher    Matcher       `json:"matcher"`
	Properties []PropertyMap `json:"properties"`
}

type Matcher struct {
	ID      string `json:"id"`
	Options string `json:"options"`
}

type PropertyMap struct {
	ID    string      `json:"id"`
	Value interface{} `json:"value"`
}

type Mapping struct {
	Type    string                 `json:"type"`
	Options map[string]interface{} `json:"options"`
}

func loadDashboard(t *testing.T) Dashboard {
	t.Helper()
	data, err := os.ReadFile("prober.json")
	if err != nil {
		t.Fatalf("failed to read prober.json: %v", err)
	}

	var d Dashboard
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("prober.json is not valid JSON: %v", err)
	}
	return d
}

func getAllPanels(panels []Panel) []Panel {
	var result []Panel
	for _, p := range panels {
		result = append(result, p)
		if len(p.Panels) > 0 {
			result = append(result, getAllPanels(p.Panels)...)
		}
	}
	return result
}

func TestProberDashboard_ValidJSON(t *testing.T) {
	d := loadDashboard(t)
	if d.Title == "" {
		t.Errorf("expected non-empty dashboard title, got empty")
	}
	if d.UID == "" {
		t.Errorf("expected non-empty dashboard UID, got empty")
	}
}

func TestProberDashboard_Timezone(t *testing.T) {
	d := loadDashboard(t)
	if d.Timezone != "browser" {
		t.Errorf("expected dashboard timezone to be 'browser', got %q", d.Timezone)
	}
}

func TestProberDashboard_TemplateVariables(t *testing.T) {
	d := loadDashboard(t)

	var repoVar *TemplateVar
	for i := range d.Templating.List {
		if d.Templating.List[i].Name == "repo" {
			repoVar = &d.Templating.List[i]
			break
		}
	}

	if repoVar == nil {
		t.Fatalf("expected template variable '$repo' not found in dashboard")
	}

	if !repoVar.Multi {
		t.Errorf("expected '$repo' variable multi to be true, got %v", repoVar.Multi)
	}
	if !repoVar.IncludeAll {
		t.Errorf("expected '$repo' variable includeAll to be true, got %v", repoVar.IncludeAll)
	}
	if repoVar.AllValue != ".*" {
		t.Errorf("expected '$repo' variable allValue to be '.*', got %q", repoVar.AllValue)
	}

	queryString := ""
	switch q := repoVar.Query.(type) {
	case string:
		queryString = q
	case map[string]interface{}:
		if queryVal, ok := q["query"].(string); ok {
			queryString = queryVal
		}
	}

	expectedQuery := "label_values(ghwebhook_prober_runs_total, repo)"
	if !strings.Contains(queryString, expectedQuery) {
		t.Errorf("expected '$repo' query to contain %q, got %q", expectedQuery, queryString)
	}
}

func TestProberDashboard_RequiredPanelsAndPromQL(t *testing.T) {
	d := loadDashboard(t)
	panels := getAllPanels(d.Panels)

	requiredQueries := []struct {
		name     string
		promQL   string
		found    bool
	}{
		{
			name:   "Total Runs",
			promQL: `sum(increase(ghwebhook_prober_runs_total{repo=~"$repo"}[$__range]))`,
		},
		{
			name:   "Successful Runs",
			promQL: `sum(increase(ghwebhook_prober_runs_total{repo=~"$repo", status="success"}[$__range]))`,
		},
		{
			name:   "Soft Failures",
			promQL: `sum(increase(ghwebhook_prober_runs_total{repo=~"$repo", status="soft_failure"}[$__range]))`,
		},
		{
			name:   "Hard Failures",
			promQL: `sum(increase(ghwebhook_prober_runs_total{repo=~"$repo", status="hard_failure"}[$__range]))`,
		},
		{
			name:   "Execution Health History",
			promQL: `sum by (status) (increase(ghwebhook_prober_runs_total{repo=~"$repo"}[$__interval]))`,
		},
		{
			name:   "Run Duration History",
			promQL: `ghwebhook_prober_last_duration_seconds{repo=~"$repo"}`,
		},
		{
			name:   "Latency Percentiles p50",
			promQL: `histogram_quantile(0.5, sum(rate(ghwebhook_prober_duration_seconds_bucket{repo=~"$repo"}[$__range])) by (le))`,
		},
		{
			name:   "Latency Percentiles p95",
			promQL: `histogram_quantile(0.95, sum(rate(ghwebhook_prober_duration_seconds_bucket{repo=~"$repo"}[$__range])) by (le))`,
		},
		{
			name:   "Latency Percentiles p99",
			promQL: `histogram_quantile(0.99, sum(rate(ghwebhook_prober_duration_seconds_bucket{repo=~"$repo"}[$__range])) by (le))`,
		},
	}

	normalizeExpr := func(s string) string {
		s = strings.ReplaceAll(s, " ", "")
		s = strings.ReplaceAll(s, "\n", "")
		s = strings.ReplaceAll(s, "\t", "")
		s = strings.ReplaceAll(s, "0.50", "0.5")
		return s
	}

	for i := range requiredQueries {
		normalizedExpected := normalizeExpr(requiredQueries[i].promQL)
		for _, panel := range panels {
			for _, target := range panel.Targets {
				if normalizeExpr(target.Expr) == normalizedExpected {
					requiredQueries[i].found = true
					break
				}
			}
			if requiredQueries[i].found {
				break
			}
		}
	}

	for _, rq := range requiredQueries {
		if !rq.found {
			t.Errorf("required PromQL target %q (%s) not found in dashboard panels", rq.name, rq.promQL)
		}
	}
}

func TestProberDashboard_MetricsCoverage(t *testing.T) {
	data, err := os.ReadFile("prober.json")
	if err != nil {
		t.Fatalf("failed to read prober.json: %v", err)
	}
	content := string(data)

	requiredMetrics := []string{
		"ghwebhook_prober_runs_total",
		"ghwebhook_prober_duration_seconds",
		"ghwebhook_prober_last_duration_seconds",
	}

	for _, metric := range requiredMetrics {
		if !strings.Contains(content, metric) {
			t.Errorf("expected dashboard to query metric %q, but was not found", metric)
		}
	}

	if !strings.Contains(content, `"prometheus"`) {
		t.Errorf("expected dashboard to reference prometheus datasource uid")
	}
}

func TestProberDashboard_ThresholdsAndColorMappings(t *testing.T) {
	d := loadDashboard(t)
	panels := getAllPanels(d.Panels)

	panelMap := make(map[string]Panel)
	for _, p := range panels {
		panelMap[p.Title] = p
	}

	// Soft Failures should have yellow threshold > 0
	if sf, ok := panelMap["Soft Failures"]; ok {
		hasYellow := false
		for _, step := range sf.FieldConfig.Defaults.Thresholds.Steps {
			if step.Color == "yellow" && step.Value != nil && *step.Value > 0 {
				hasYellow = true
			}
		}
		if !hasYellow {
			t.Errorf("expected Soft Failures panel to have yellow threshold step > 0")
		}
	} else {
		t.Errorf("panel 'Soft Failures' not found")
	}

	// Hard Failures should have red threshold > 0
	if hf, ok := panelMap["Hard Failures"]; ok {
		hasRed := false
		for _, step := range hf.FieldConfig.Defaults.Thresholds.Steps {
			if step.Color == "red" && step.Value != nil && *step.Value > 0 {
				hasRed = true
			}
		}
		if !hasRed {
			t.Errorf("expected Hard Failures panel to have red threshold step > 0")
		}
	} else {
		t.Errorf("panel 'Hard Failures' not found")
	}

	// Run Duration History should have threshold steps: Green (< 30s), Yellow (30s - 50s), Red (>= 50s)
	if rd, ok := panelMap["Run Duration History"]; ok {
		hasYellow30 := false
		hasRed50 := false
		for _, step := range rd.FieldConfig.Defaults.Thresholds.Steps {
			if step.Color == "yellow" && step.Value != nil && *step.Value == 30 {
				hasYellow30 = true
			}
			if step.Color == "red" && step.Value != nil && *step.Value == 50 {
				hasRed50 = true
			}
		}
		if !hasYellow30 || !hasRed50 {
			t.Errorf("expected Run Duration History to have threshold bands at 30s (yellow) and 50s (red)")
		}
	} else {
		t.Errorf("panel 'Run Duration History' not found")
	}

	// Execution Health History should have status overrides for success (green), soft_failure (yellow), hard_failure (red)
	if eh, ok := panelMap["Execution Health History"]; ok {
		mapped := make(map[string]bool)
		for _, override := range eh.FieldConfig.Overrides {
			if override.Matcher.ID == "byName" {
				mapped[override.Matcher.Options] = true
			}
		}
		if !mapped["success"] || !mapped["soft_failure"] || !mapped["hard_failure"] {
			t.Errorf("expected status overrides for success, soft_failure, and hard_failure, got %+v", mapped)
		}
	} else {
		t.Errorf("panel 'Execution Health History' not found")
	}
}
