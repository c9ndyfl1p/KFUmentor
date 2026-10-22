package browser

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/llm"
	"github.com/mihnpro/Ai_agent_2/internal/types"
)

func TestSearchStopsAtSuitableVacancyLimit(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		target, minimum       int
		outcomes              []string
		wantChecked, wantFits int
	}{
		{"one_from_three", 1, 6, []string{"fit", "fit", "fit"}, 1, 1},
		{"two_from_four", 2, 6, []string{"fit", "fit", "fit", "fit"}, 2, 2},
		{"last_batch_has_one_slot", 4, 6, []string{"fit", "fit", "fit", "fit", "fit", "fit"}, 4, 4},
		{"low_score_does_not_use_slot", 2, 6, []string{"low", "fit", "fit", "fit"}, 3, 2},
		{"errors_do_not_use_slots", 2, 6, []string{"error", "low", "fit", "fit", "fit"}, 4, 2},
		{"fewer_matches_than_target", 4, 6, []string{"fit", "fit", "fit"}, 3, 3},
		{"no_matches", 2, 6, []string{"low", "low", "low"}, 3, 0},
		{"score_equals_threshold", 2, 8, []string{"fit", "fit", "fit"}, 2, 2},
		{"high_threshold_is_preserved", 2, 10, []string{"fit", "fit", "fit"}, 3, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KFUMENTOR_WORKFLOW_PATH", filepath.Join(t.TempDir(), "history.json"))
			t.Setenv("AI_JOB_AGENT_MODEL_REQUEST_INTERVAL_SECONDS", "0")
			outcomes := map[string]string{}
			calls := map[string]int{}
			var mu sync.Mutex
			a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
				var request llm.GenerateRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				var input struct {
					Vacancy types.Job `json:"vacancy"`
				}
				if err := json.Unmarshal([]byte(request.Prompt), &input); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				mu.Lock()
				calls[input.Vacancy.URL]++
				mu.Unlock()
				switch outcomes[input.Vacancy.URL] {
				case "fit":
					json.NewEncoder(w).Encode(llm.GenerateResponse{Response: `{"role_match":"direct","mandatory_gaps":[],"is_relevant":true,"match_score":8,"recommendation":"apply","reason":"SQL подтверждён"}`})
				case "low":
					json.NewEncoder(w).Encode(llm.GenerateResponse{Response: `{"role_match":"different","mandatory_gaps":[],"is_relevant":false,"match_score":2,"recommendation":"skip","reason":"Другое направление"}`})
				case "error":
					w.WriteHeader(http.StatusBadGateway)
				default:
					t.Error("unexpected model request", input.Vacancy.URL)
					w.WriteHeader(http.StatusBadRequest)
				}
			})
			a.vacancyLimit, a.minimumScore = tc.target, tc.minimum
			a.resume = types.Resume{Skills: []string{"SQL"}}
			a.currentTask = "SQL аналитик"
			for i, outcome := range tc.outcomes {
				job := targetJob(i + 1)
				job.Snippet = "Требуется SQL"
				outcomes[job.URL] = outcome
				a.lastEvaluatedJobs = append(a.lastEvaluatedJobs, job)
				a.cacheJobDetails(job.URL, job)
			}

			result, err := a.evaluateAllJobsOnPage()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result, fmt.Sprintf("Подтверждено от %d/10: %d из %d", tc.minimum, tc.wantFits, tc.target)) {
				t.Fatal("wrong search summary:", result)
			}
			if a.vacanciesAnalyzed != tc.wantChecked || len(calls) != tc.wantChecked {
				t.Fatalf("evaluated %d vacancies, requested %d unique model assessments; want %d", a.vacanciesAnalyzed, len(calls), tc.wantChecked)
			}
			state, err := currentStore().Read()
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Entries) != tc.wantChecked {
				t.Fatalf("saved %d cards, want %d", len(state.Entries), tc.wantChecked)
			}
			fits := 0
			for _, entry := range state.Entries {
				if entry.Status != "error" && entry.Score >= tc.minimum {
					fits++
				}
				if entry.Submission != "" {
					t.Fatal("search submitted an application", entry.ID)
				}
			}
			if fits != tc.wantFits {
				t.Fatalf("saved %d suitable cards, want %d", fits, tc.wantFits)
			}
			for i := tc.wantChecked; i < len(tc.outcomes); i++ {
				if calls[targetJob(i+1).URL] != 0 {
					t.Fatal("requested an unnecessary assessment after reaching the goal", i+1)
				}
			}
		})
	}
}
