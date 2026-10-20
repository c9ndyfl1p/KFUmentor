package browser

import (
	"encoding/json"
	"errors"
	"github.com/mihnpro/Ai_agent_2/internal/workflow"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/llm"
	"github.com/mihnpro/Ai_agent_2/internal/types"
)

func TestEvaluationEvidenceRules(t *testing.T) {
	job := types.Job{Title: "Кредитные операции", ExperienceRequirement: "не требуется", Location: "Чебоксары", Snippet: "Обязанности:\nВвод данных в банковские системы (АБС).\nТребования:\nУверенное владение MS Office (Word, Excel, Outlook).\nВысокая скорость печати будет вашим преимуществом.\nМы предлагаем:\nРабота в офисе в Чебоксарах."}
	profile := types.Resume{Skills: []string{"Excel"}}
	context := types.SearchContext{AreaID: 107, AreaName: "Чебоксары"}
	tests := []struct {
		name   string
		result JobEvaluationResult
		valid  bool
	}{
		{"direct", JobEvaluationResult{RoleMatch: "direct", Recommendation: "apply", MatchScore: 8}, true},
		{"related", JobEvaluationResult{RoleMatch: "related", Recommendation: "review", MatchScore: 6}, true},
		{"related_advisory_score", JobEvaluationResult{RoleMatch: "related", Recommendation: "skip", MatchScore: 3}, true},
		{"unknown_required_skill", JobEvaluationResult{RoleMatch: "direct", Recommendation: "review", MatchScore: 6, Gaps: []types.RequirementGap{{Kind: "skill", Status: "unknown", VacancyQuote: "Уверенное владение MS Office (Word, Excel, Outlook)."}}}, true},
		{"optional", JobEvaluationResult{RoleMatch: "direct", Recommendation: "review", MatchScore: 6, Gaps: []types.RequirementGap{{Kind: "skill", Status: "unknown", VacancyQuote: "Высокая скорость печати будет вашим преимуществом."}}}, false},
		{"duty_is_not_requirement", JobEvaluationResult{RoleMatch: "direct", Recommendation: "review", MatchScore: 6, Gaps: []types.RequirementGap{{Kind: "skill", Status: "unknown", VacancyQuote: "Ввод данных в банковские системы (АБС)."}}}, false},
		{"invented_requirement", JobEvaluationResult{RoleMatch: "direct", Recommendation: "skip", MatchScore: 3, Gaps: []types.RequirementGap{{Kind: "experience", Status: "conflict", VacancyQuote: "Опыт работы с кредитами от года", CandidateQuote: "нет опыта"}}}, false},
		{"chosen_city", JobEvaluationResult{RoleMatch: "direct", Recommendation: "review", MatchScore: 6, Gaps: []types.RequirementGap{{Kind: "location", Status: "unknown", VacancyQuote: "Работа в офисе в Чебоксарах."}}}, false},
		{"absence_is_not_conflict", JobEvaluationResult{RoleMatch: "direct", Recommendation: "skip", MatchScore: 3, Gaps: []types.RequirementGap{{Kind: "skill", Status: "conflict", VacancyQuote: "Уверенное владение MS Office (Word, Excel, Outlook)."}}}, false},
		{"wrong_role", JobEvaluationResult{RoleMatch: "different", Recommendation: "skip", MatchScore: 2}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEvaluationFacts(&tc.result, job, profile, "аналитик", context)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}

func TestEvaluationSendsHeaderAndSelectedCityAndRepairsInvalidCityGap(t *testing.T) {
	calls := 0
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req llm.GenerateRequest
		json.NewDecoder(r.Body).Decode(&req)
		var input struct {
			Vacancy  types.Job           `json:"vacancy"`
			Context  types.SearchContext `json:"search_context"`
			Previous string              `json:"previous_evaluation"`
			Problem  string              `json:"validation_error"`
		}
		json.Unmarshal([]byte(req.Prompt), &input)
		if input.Vacancy.ExperienceRequirement != "не требуется" || input.Context.AreaName != "Чебоксары" || input.Context.AreaID != 107 {
			t.Error("missing header or city", input)
		}
		response := `{"is_relevant":true,"role_match":"related","mandatory_gaps":[],"match_score":6,"recommendation":"review","reason":"Смежная операционная роль"}`
		if calls == 1 {
			response = `{"is_relevant":true,"role_match":"related","mandatory_gaps":[{"kind":"location","status":"unknown","vacancy_quote":"Чебоксары","candidate_quote":""}],"match_score":6,"recommendation":"review","reason":"Нужна проверка города"}`
		}
		if calls == 2 && !strings.Contains(req.System, "Ошибка проверки:") {
			t.Error("retry omitted exact problem")
		}
		if calls == 2 && (!strings.Contains(input.Previous, `"kind":"location"`) || !strings.Contains(input.Problem, "город вакансии совпадает")) {
			t.Error("retry omitted rejected answer or validation problem")
		}
		json.NewEncoder(w).Encode(llm.GenerateResponse{Response: response})
	})
	job := types.Job{URL: "https://hh.ru/vacancy/1", ExperienceRequirement: "не требуется", Location: "Чебоксары", Snippet: "Требуется Excel"}
	a.jobDetails[job.URL] = job
	a.resume = types.Resume{Skills: []string{"Excel"}}
	a.areaID = 107
	a.areaName = "Чебоксары"
	result, err := a.evaluateJobForTaskWithAnalysis(job, "аналитик")
	if err != nil || calls != 2 || result.MatchScore != 6 {
		t.Fatal(result, calls, err)
	}
}

func TestInvalidEvidenceTwiceDoesNotProduceUsableScore(t *testing.T) {
	calls := 0
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(llm.GenerateResponse{Response: `{"is_relevant":true,"role_match":"direct","mandatory_gaps":[],"match_score":3,"recommendation":"skip"}`})
	})
	job := types.Job{URL: "https://hh.ru/vacancy/1", Snippet: "SQL"}
	a.jobDetails[job.URL] = job
	a.resume = types.Resume{Skills: []string{"SQL"}}
	result, err := a.evaluateJobForTaskWithAnalysis(job, "аналитик")
	if err == nil || result != nil || calls != 2 {
		t.Fatal(result, calls, err)
	}
}

func TestInterpretationStableAndUsesOnlyVerifiedFindings(t *testing.T) {
	job := types.Job{ExperienceRequirement: "не требуется"}
	profile := types.Resume{Skills: []string{"Excel"}}
	for _, raw := range []int{3, 5, 6, 7} {
		e := JobEvaluationResult{RoleMatch: "related", MatchScore: raw, Recommendation: "review", Gaps: []types.RequirementGap{{Kind: "skill", Status: "unknown", VacancyQuote: "Уверенное владение Word и Outlook"}}, ReviewPoints: []string{"Готовы ли к офису в Чебоксарах?"}, Analysis: "У кандидата нет стажа", SkillsMatch: []string{"Excel", "Внимание к деталям"}}
		interpretEvaluation(&e, job, profile)
		if e.ModelScore != raw || e.MatchScore != 5 || e.Recommendation != "review" {
			t.Fatal(e)
		}
		text := strings.Join(e.ReviewPoints, " ") + e.Analysis
		if strings.Contains(text, "Готовы ли к офису") || strings.Contains(text, "нет стажа") || len(e.SkillsMatch) != 1 || e.SkillsMatch[0] != "Excel" {
			t.Fatal(e)
		}
	}
	experience := JobEvaluationResult{RoleMatch: "direct", MatchScore: 6, Recommendation: "review", Gaps: []types.RequirementGap{{Kind: "experience", Status: "unknown", VacancyQuote: "Обязательно 5 лет опыта"}}}
	interpretEvaluation(&experience, types.Job{}, profile)
	if experience.MatchScore != 5 || experience.Recommendation != "review" {
		t.Fatal(experience)
	}
}

func TestHighScoreThresholdsRemainAvailable(t *testing.T) {
	for _, score := range []int{7, 8, 9, 10} {
		e := JobEvaluationResult{RoleMatch: "direct", MatchScore: score, Recommendation: "apply"}
		interpretEvaluation(&e, types.Job{}, types.Resume{})
		if e.MatchScore != score {
			t.Fatal("strong fit score changed", e)
		}
	}
}

func TestFailedReevaluationClearsOldInterpretation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	t.Setenv("KFUMENTOR_WORKFLOW_PATH", path)
	store := &workflow.Store{Path: path}
	job := types.Job{Title: "Analyst", URL: "https://hh.ru/vacancy/1", DescriptionStatus: "ready"}
	if err := store.Update(func(st *workflow.State) error {
		st.Entries["1"] = &workflow.Entry{ID: "1", Job: job, Score: 6, ModelScore: 7, RoleMatch: "related", Recommendation: "review", Reason: "Old", ReviewPoints: []string{"Old question"}, Gaps: []types.RequirementGap{{Kind: "skill"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a := &AIOrchestrator{}
	if err := a.rememberJob(job, nil, errors.New("bad model response"), false); err != nil {
		t.Fatal(err)
	}
	st, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	e := st.Entries["1"]
	if e.Score != 0 || e.ModelScore != 0 || e.RoleMatch != "" || e.Recommendation != "" || e.Reason != "" || len(e.ReviewPoints) != 0 || len(e.Gaps) != 0 || e.Status != "error" {
		t.Fatal(e)
	}
}

func TestTrainingOfferDoesNotEraseMandatoryRequirement(t *testing.T) {
	job := types.Job{Snippet: "Требования:\nОпыт работы от 2 лет, остальному обучаем."}
	profile := types.Resume{Summary: "Коммерческого опыта нет"}
	e := JobEvaluationResult{RoleMatch: "direct", MatchScore: 3, Recommendation: "skip", Gaps: []types.RequirementGap{{Kind: "experience", Status: "conflict", VacancyQuote: "Опыт работы от 2 лет", CandidateQuote: "Коммерческого опыта нет"}}}
	if err := validateEvaluationFacts(&e, job, profile, "аналитик", types.SearchContext{}); err != nil {
		t.Fatal(err)
	}
}
