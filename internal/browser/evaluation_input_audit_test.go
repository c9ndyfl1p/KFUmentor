package browser

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mihnpro/Ai_agent_2/internal/llm"
	"github.com/mihnpro/Ai_agent_2/internal/types"
)

// Explicit opt-in, no browser, no workflow/history writes, no applications.
func TestDeepSeekInputAudit(t *testing.T) {
	if os.Getenv("KFUMENTOR_INPUT_AUDIT") != "1" {
		t.Skip("opt-in real DeepSeek input audit")
	}
	var input struct {
		Profile types.Resume `json:"profile"`
		Cases   []struct {
			Context types.SearchContext `json:"search_context"`
			ID      string              `json:"id"`
			Job     types.Job           `json:"job"`
			Task    string              `json:"task"`
			Repeats int                 `json:"repeats"`
			Expect  string              `json:"expect"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(os.Getenv("KFUMENTOR_AUDIT_INPUT"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("KFUMENTOR_AUDIT_OUTPUT")
	if path == "" {
		t.Fatal("output path required")
	}
	type observation struct {
		ID         string               `json:"id"`
		Repeat     int                  `json:"repeat"`
		Seconds    float64              `json:"seconds"`
		Evaluation *JobEvaluationResult `json:"evaluation,omitempty"`
		Error      string               `json:"error,omitempty"`
	}
	var observations []observation
	for _, tc := range input.Cases {
		if filter := os.Getenv("KFUMENTOR_AUDIT_FILTER"); filter != "" && !strings.Contains(","+filter+",", ","+tc.ID+",") {
			continue
		}
		for repeat := 1; repeat <= tc.Repeats; repeat++ {
			client := llm.NewOllamaClient("http://127.0.0.1:8000/v1", "deepseek-chat")
			a := &AIOrchestrator{areaID: tc.Context.AreaID, areaName: tc.Context.AreaName, llmClient: client, resume: input.Profile, jobDetails: map[string]types.Job{tc.Job.URL: tc.Job}}
			started := time.Now()
			result, err := a.evaluateJobForTaskWithAnalysis(tc.Job, tc.Task)
			row := observation{ID: tc.ID, Repeat: repeat, Seconds: time.Since(started).Seconds(), Evaluation: result}
			if err != nil {
				row.Error = err.Error()
			}
			observations = append(observations, row)
			output, _ := json.MarshalIndent(observations, "", "  ")
			if writeErr := os.WriteFile(path, output, 0600); writeErr != nil {
				t.Fatal(writeErr)
			}
			if err != nil {
				t.Fatalf("%s stopped after model error: %v", tc.ID, err)
			}
			t.Logf("%s #%d: %d/10 %s; %s", tc.ID, repeat, result.MatchScore, result.Recommendation, result.Reason)
		}
	}
}
