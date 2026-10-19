package browser

import (
	"encoding/json"
	"errors"
	"github.com/mihnpro/Ai_agent_2/internal/llm"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/workflow"
)

func TestPartialApplicationIsCountedAndNotRetried(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	t.Setenv("KFUMENTOR_WORKFLOW_PATH", path)
	t.Setenv("KFUMENTOR_RUN_ID", "run")
	store := currentStore()
	store.Update(func(st *workflow.State) error {
		st.Entries["1"] = &workflow.Entry{ID: "1", Submission: "sending"}
		st.Runs["run"] = &workflow.Run{Target: 3}
		return nil
	})
	a := &AIOrchestrator{}
	err := a.finishSubmission(targetJob(1), "run", "", &ApplicationFailure{Err: errors.New("field not found"), Sent: true, Uncertain: true})
	st, _ := store.Read()
	if err != nil || st.Runs["run"].Sent != 1 || st.Entries["1"].Submission != "without_letter" {
		t.Fatal(st, err)
	}
	if err := a.reserveSubmission(targetJob(1)); !errors.Is(err, errAlreadyApplied) {
		t.Fatal(err)
	}
}
func TestManualCheckpointConsumesGoalBeforeNextAutoSubmit(t *testing.T) {
	sent, calls := 0, 0
	summary, err := runApplicationTarget(1, []string{"analyst"}, applicationOperations{
		search:     func(string, int) ([]Job, error) { return []Job{targetJob(1)}, nil },
		checkpoint: func() error { sent++; return nil }, totalSent: func() int { return sent },
		evaluate: relevantJob, submit: func(Job) (string, error) { calls++; return "", nil },
	})
	if err != nil || summary.Sent != 1 || calls != 0 {
		t.Fatal(summary, calls, err)
	}
}
func TestEvaluationRejectsMissingAndInvalidDecisionFields(t *testing.T) {
	for _, body := range []string{`{}`, `{"is_relevant":"true","match_score":7}`, `{"is_relevant":true,"match_score":11,"recommendation":"apply"}`, `{"is_relevant":true,"match_score":7,"recommendation":"yes"}`} {
		var e JobEvaluationResult
		if err := json.Unmarshal([]byte(body), &e); err == nil && validateEvaluation(&e) == nil {
			t.Fatal("invalid evaluation accepted", body)
		}
	}
}

func TestQueuedDraftOnlyPreparesLetterAndChangedProfileBlocksSend(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KFUMENTOR_WORKFLOW_PATH", filepath.Join(dir, "history.json"))
	profilePath := filepath.Join(dir, "resume.json")
	if err := os.WriteFile(profilePath, []byte(`{"name":"Кандидат","title":"Аналитик","summary":"Собираю требования","skills":["SQL"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
		var request llm.GenerateRequest
		json.NewDecoder(r.Body).Decode(&request)
		response := "Здравствуйте! Рассматриваю вакансию аналитика. Среди моих навыков SQL. Готов обсудить задачи позиции."
		if request.Format == "json" {
			response = `{"supported":true,"unsupported_claims":[]}`
		}
		json.NewEncoder(w).Encode(llm.GenerateResponse{Response: response})
	})
	a.resumePath = profilePath
	a.letterExamplesPath = filepath.Join(dir, "examples.json")
	job := targetJob(1)
	a.jobDetails[job.URL] = job
	store := currentStore()
	store.Update(func(st *workflow.State) error {
		st.Entries["1"] = &workflow.Entry{ID: "1", Job: job, RunID: "run", Status: "review", Recommendation: "review", Score: 7, Threshold: 6}
		st.Runs["run"] = &workflow.Run{Target: 2}
		return nil
	})
	if err := store.Enqueue("1", "draft", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.drainManualQueue(); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Read()
	entry := st.Entries["1"]
	if entry.LetterValidation == "" || entry.LetterValidation != letterValidation(entry.ProfileHash, entry.Letter) {
		t.Fatal("checked draft was not bound to profile and letter")
	}
	if entry.Status != "draft_ready" || entry.Letter == "" || entry.Version == "" || entry.Submission != "" || st.Runs["run"].Sent != 0 {
		t.Fatal(entry)
	}
	if err := store.Enqueue("1", "send", entry.Version); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(profilePath, []byte(`{"name":"Кандидат","skills":["Python"]}`), 0600)
	if err := a.drainManualQueue(); err != nil {
		t.Fatal(err)
	}
	st, _ = store.Read()
	if st.Entries["1"].Submission != "" || st.Entries["1"].Status != "draft_error" || st.Runs["run"].Sent != 0 {
		t.Fatal(st)
	}
}

func TestLetterValidationChangesWithTextAndProfile(t *testing.T) {
	mark := letterValidation("profile1", "letter1")
	if mark == letterValidation("profile2", "letter1") || mark == letterValidation("profile1", "letter2") {
		t.Fatal("validation marker reused after an input changed")
	}
}

func TestSubmissionReceiptIsIdempotent(t *testing.T) {
	t.Setenv("KFUMENTOR_WORKFLOW_PATH", filepath.Join(t.TempDir(), "history.json"))
	store := currentStore()
	store.Update(func(st *workflow.State) error {
		st.Entries["1"] = &workflow.Entry{ID: "1", Submission: "sending"}
		st.Runs["run"] = &workflow.Run{Target: 3}
		return nil
	})
	a := &AIOrchestrator{}
	for i := 0; i < 2; i++ {
		if err := a.finishSubmission(targetJob(1), "run", "confirmed", nil); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := store.Read()
	if st.Runs["run"].Sent != 1 {
		t.Fatal("duplicate receipt counted twice", st.Runs["run"])
	}
}

func TestManualDraftReviewFailureAllowsFactualPreviewButAutoStillBlocks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KFUMENTOR_WORKFLOW_PATH", filepath.Join(dir, "history.json"))
	profilePath := filepath.Join(dir, "resume.json")
	os.WriteFile(profilePath, []byte(`{"summary":"Начинающий аналитик.","skills":["SQL"]}`), 0600)
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
		var req llm.GenerateRequest
		json.NewDecoder(r.Body).Decode(&req)
		text := "Здравствуйте! У меня пять лет коммерческого опыта и я эксперт в Python. Готов обсудить вакансию."
		if req.Format == "json" {
			text = `{"wrong_field":true}`
		}
		json.NewEncoder(w).Encode(llm.GenerateResponse{Response: text})
	})
	a.resumePath = profilePath
	a.resume = loadResume(profilePath)
	job := targetJob(1)
	job.Title = "Аналитик"
	job.Snippet = "SQL; Python обязателен"
	a.jobDetails[job.URL] = job
	if _, err := a.generateCoverLetter(job.Title, job.Company, job); err == nil {
		t.Fatal("automatic path ignored failed review")
	}
	store := currentStore()
	store.Update(func(st *workflow.State) error {
		st.Entries["1"] = &workflow.Entry{ID: "1", Job: job, Status: "review", Recommendation: "review", Score: 6, Threshold: 6, ReviewPoints: []string{"Python не указан"}}
		return nil
	})
	if err := store.Enqueue("1", "draft", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.drainManualQueue(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	e := st.Entries["1"]
	if e.Status != "draft_ready" || e.LetterWarning == "" || e.LetterValidation == "" || e.Error != "" {
		t.Fatal(e)
	}
	if strings.Contains(e.Letter, "пять лет") || strings.Contains(e.Letter, "эксперт") || !strings.Contains(e.Letter, "SQL") {
		t.Fatal("unsafe claims leaked into fallback", e.Letter)
	}
	if err := store.Enqueue("1", "send", e.Version); err != nil {
		t.Fatal("manual confirmation blocked by missing skill", err)
	}
}
