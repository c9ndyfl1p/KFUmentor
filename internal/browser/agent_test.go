package browser

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/llm"
	"github.com/mihnpro/Ai_agent_2/internal/prompt"
	"github.com/mihnpro/Ai_agent_2/internal/types"
	playwright "github.com/mxschmitt/playwright-go"
)

func TestFallbackSearch(t *testing.T) {
	for _, task := range []string{"Хочу найти вакансии на машинное обучение", "хочу работу с машинным обучением", "машинное машинное обучение"} {
		queries := fallbackSearchQueries(task)
		if len(queries) != 3 || queries[0] != "ML Engineer" {
			t.Fatalf("%s: %v", task, queries)
		}
	}
	if got := extractQueryFromTask("найди вакансии C++ C++ .NET Python3"); got != "C++ .NET Python3" {
		t.Fatal(got)
	}
	if got := fallbackSearchQueries("junior машинное обучение")[0]; got != "junior ML Engineer" {
		t.Fatal(got)
	}
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockAgent(t *testing.T, handler http.HandlerFunc) *AIOrchestrator {
	t.Helper()
	client := llm.NewOllamaClient("http://local-model.test", "test")
	client.HTTPClient.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler(recorder, r)
		return recorder.Result(), nil
	})
	return &AIOrchestrator{llmClient: client, jobDetails: map[string]types.Job{}, letterExamplesPath: filepath.Join(t.TempDir(), "examples.json")}
}

func TestPlanUsesModelQueriesAndExplicitApplicationPermission(t *testing.T) {
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
		var request llm.GenerateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Format != "json" || !strings.Contains(request.System, "поисковые запросы") {
			t.Error("missing structured search prompt")
		}
		json.NewEncoder(w).Encode(llm.GenerateResponse{Response: `{"queries":["ML Engineer","ML Engineer","машинное обучение"]}`})
	})
	plan, err := a.createPlan("найди 3 вакансии машинного обучения, отправь отклики", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 5 || plan[0].Parameters["query"] != "ML Engineer" || plan[4].Parameters["count"] != float64(3) {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	for _, step := range plan {
		if step.Action == "apply_to_relevant" || step.Action == "apply_until_target" {
			t.Fatal("user text must not enable submissions")
		}
	}
	plan, err = a.createPlan("ML Engineer с зарплатой 20 тысяч", true)
	if err != nil || len(plan) != 1 || plan[0].Action != "apply_until_target" {
		t.Fatalf("unexpected plan: %+v %v", plan, err)
	}
}

func TestInvalidModelSearchFallsBack(t *testing.T) {
	for _, response := range []string{`{"queries":[]}`, `broken`, `{"queries":["https://hh.ru"]}`} {
		t.Run(response, func(t *testing.T) {
			a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(llm.GenerateResponse{Response: response})
			})
			if queries := a.searchQueries("работа с машинным обучением"); len(queries) != 3 || queries[0] != "ML Engineer" {
				t.Fatal(queries)
			}
		})
	}
}

func TestSearchResultsIncludeSynonymsWithoutDuplicates(t *testing.T) {
	groups := [][]types.Job{{{URL: "a"}, {URL: "b"}, {URL: "c"}}, {{URL: "a"}, {URL: "d"}}, {{URL: "e"}}}
	jobs := selectSearchResults(groups, 4)
	var urls []string
	for _, job := range jobs {
		urls = append(urls, job.URL)
	}
	if strings.Join(urls, ",") != "a,e,b,d" {
		t.Fatal(urls)
	}
}

func TestLetterContainsVacancyResumeAndOptionalExamples(t *testing.T) {
	job := types.Job{Title: "ML Engineer", Company: "Example", URL: "https://hh.ru/vacancy/1", Snippet: "Python; требуется классификация текстов"}
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
		var request llm.GenerateRequest
		json.NewDecoder(r.Body).Decode(&request)
		if request.System == prompt.LetterReviewSystem {
			json.NewEncoder(w).Encode(llm.GenerateResponse{Response: `{"supported":true,"unsupported_claims":[]}`})
			return
		}
		var data struct {
			Resume   types.Resume    `json:"resume"`
			Vacancy  types.Job       `json:"vacancy"`
			Examples []letterExample `json:"style_examples"`
		}
		if err := json.Unmarshal([]byte(request.Prompt), &data); err != nil {
			t.Error(err)
		}
		if data.Vacancy.Snippet != job.Snippet || data.Resume.Summary != "Учебный NLP-проект" || len(data.Resume.Experience) != 1 || len(data.Examples) != 1 {
			t.Errorf("missing source data: %+v", data)
		}
		if data.Resume.ContactInfo.Email != "" || !strings.Contains(request.System, "Не выдумывай") {
			t.Error("missing factuality instructions or contacts leaked")
		}
		json.NewEncoder(w).Encode(llm.GenerateResponse{Response: "Здравствуйте! Меня заинтересовала вакансия ML Engineer. В учебном проекте использовал Python для классификации текстов. Предлагаю обсудить мой опыт."})
	})
	a.jobDetails[job.URL] = job
	a.resume = types.Resume{Name: "Тест", Summary: "Учебный NLP-проект", Skills: []string{"Python"}, Experience: []types.Experience{{Position: "Учебный проект"}}, ContactInfo: types.Contact{Email: "private@example.com"}}
	if err := os.WriteFile(a.letterExamplesPath, []byte(`[{"vacancy":"другая вакансия","letter":"Письмо для примера стиля"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.generateCoverLetter(job.Title, job.Company, job); err != nil {
		t.Fatal(err)
	}
}

func TestRejectInvalidLetterAndExampleFiles(t *testing.T) {
	for _, text := range []string{"", "<think>analysis", "```text\nЗдравствуйте, длинное письмо с неправильной разметкой```"} {
		if _, err := cleanCoverLetter(text); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
	text, err := cleanCoverLetter("<think>analysis</think>\nЗдравствуйте! Предлагаю обсудить мою кандидатуру на эту вакансию.")
	if err != nil || strings.Contains(text, "think") {
		t.Fatal(text, err)
	}
	path := filepath.Join(t.TempDir(), "examples.json")
	if examples, err := loadLetterExamples(path); err != nil || len(examples) != 0 {
		t.Fatal(examples, err)
	}
	for _, content := range []string{"broken", `[{"letter":""}]`, strings.Repeat("x", 65537)} {
		os.WriteFile(path, []byte(content), 0600)
		if _, err := loadLetterExamples(path); err == nil {
			t.Fatal("accepted invalid examples")
		}
	}
}

func TestEvaluationUsesSummaryAndRejectsInvalidScore(t *testing.T) {
	for _, score := range []int{8, 80} {
		a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
			var request llm.GenerateRequest
			json.NewDecoder(r.Body).Decode(&request)
			if !strings.Contains(request.Prompt, "Учебный NLP-проект") {
				t.Error("evaluation omitted resume summary")
			}
			if !strings.Contains(request.System, prompt.EvaluationSystem) {
				t.Error("evaluation or JSON retry omitted scoring rules")
			}
			json.NewEncoder(w).Encode(llm.GenerateResponse{Response: fmt.Sprintf(`{"is_relevant":true,"match_score":%d,"recommendation":"review"}`, score)})
		})
		job := types.Job{Title: "ML Engineer", URL: "https://hh.ru/vacancy/1", Snippet: "Python"}
		a.jobDetails[job.URL] = job
		a.resume.Summary = "Учебный NLP-проект"
		_, err := a.evaluateJobForTaskWithAnalysis(job, "ML Engineer")
		if (score == 80) != (err != nil) {
			t.Fatalf("score %d: %v", score, err)
		}
	}
}

func TestUnsupportedLetterIsRewrittenThenFallsBackToFacts(t *testing.T) {
	generated, reviewed := 0, 0
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
		var request llm.GenerateRequest
		json.NewDecoder(r.Body).Decode(&request)
		response := "Здравствуйте! У меня пять лет коммерческого опыта и я руководил командой разработчиков."
		if request.System == prompt.LetterReviewSystem {
			reviewed++
			response = `{"supported":false,"unsupported_claims":["пять лет коммерческого опыта"]}`
		} else {
			generated++
		}
		json.NewEncoder(w).Encode(llm.GenerateResponse{Response: response})
	})
	a.resume = types.Resume{Summary: "Выполнил учебный проект", Skills: []string{"Python"}}
	job := types.Job{Title: "ML Engineer", URL: "https://hh.ru/vacancy/1", Snippet: "Требуется Python"}
	a.jobDetails[job.URL] = job
	letter, err := a.generateCoverLetter(job.Title, job.Company, job)
	if err != nil || generated != 2 || reviewed != 2 || strings.Contains(letter, "пять лет") || !strings.Contains(letter, "Python") {
		t.Fatal(letter, generated, reviewed, err)
	}
}

func TestBrowserDescriptionSmoke(t *testing.T) {
	if os.Getenv("KFUMENTOR_BROWSER_SMOKE") != "1" {
		t.Skip("opt-in Chromium fixture test")
	}
	pw, err := playwright.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Stop()
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	if err := page.SetContent(`<html><body><h1>ML Engineer</h1><div data-qa="vacancy-company-name">Тестовая компания</div><div data-qa="vacancy-description">Требуется Python. Учебный проект по классификации текстов.</div></body></html>`); err != nil {
		t.Fatal(err)
	}
	job, err := readJobDetails(page, types.Job{URL: "https://hh.ru/vacancy/1"})
	if err != nil || job.Title != "ML Engineer" || job.Company != "Тестовая компания" || !strings.Contains(job.Snippet, "классификации текстов") {
		t.Fatal(job, err)
	}
}

// Opt-in smoke test uses a synthetic profile and never opens hh.ru or sends applications.
func TestLocalModelSmoke(t *testing.T) {
	if os.Getenv("KFUMENTOR_MODEL_SMOKE") != "1" {
		t.Skip("set KFUMENTOR_MODEL_SMOKE=1 for local Ollama")
	}
	a := &AIOrchestrator{llmClient: llm.NewOllamaClient("http://127.0.0.1:11434", "qwen3.5:4b-q4_K_M"), letterExamplesPath: filepath.Join(t.TempDir(), "missing.json"), jobDetails: map[string]types.Job{}}
	data, _ := json.Marshal(map[string]string{"task": "Хочу найти вакансии на машинное обучение"})
	response, err := a.llmClient.GenerateJSON(string(data), prompt.SearchSystem)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Queries []string `json:"queries"`
	}
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		t.Fatal(response, err)
	}
	queries := cleanSearchQueries(result.Queries)
	if len(queries) == 0 {
		t.Fatal(response)
	}
	t.Logf("model search queries: %v", queries)
	job := types.Job{Title: "Junior ML Engineer", Company: "Тестовая компания", URL: "https://hh.ru/vacancy/1", Snippet: "Требуется Python и опыт учебных проектов по классификации текстов. Коммерческий опыт необязателен."}
	a.jobDetails[job.URL] = job
	a.resume = types.Resume{Name: "Тестовый кандидат", Title: "Начинающий ML-инженер", Skills: []string{"Python", "scikit-learn"}, Summary: "Выполнил учебный проект по классификации текстов. Коммерческого опыта нет."}
	evaluation, err := a.evaluateJobForTaskWithAnalysis(job, "найди работу начинающего ML-инженера")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("model evaluation: %d/10, %s", evaluation.MatchScore, evaluation.Recommendation)
	letter, err := a.generateCoverLetter(job.Title, job.Company, job)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("draft with synthetic profile: %s", letter)
}

func TestEvaluationDoesNotDiscardLateRequirementsOrSkills(t *testing.T) {
	tail := "Обязателен коммерческий стаж не менее 5 лет, учебные проекты стаж не заменяют."
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
		var request llm.GenerateRequest
		json.NewDecoder(r.Body).Decode(&request)
		if !strings.Contains(request.Prompt, tail) {
			t.Error("mandatory requirement at end was discarded")
		}
		if !strings.Contains(request.Prompt, "Шестая релевантная работа") {
			t.Error("experience after first five was discarded")
		}
		if !strings.Contains(request.Prompt, "Power BI") {
			t.Error("skill after first 30 was discarded")
		}
		if strings.Contains(request.Prompt, "private@example.test") {
			t.Error("contact details sent to evaluation model")
		}
		json.NewEncoder(w).Encode(llm.GenerateResponse{Response: `{"is_relevant":true,"match_score":3,"recommendation":"skip"}`})
	})
	a.resume = types.Resume{Summary: "Коммерческого стажа нет", ContactInfo: types.Contact{Email: "private@example.test"}}
	for i := 0; i < 35; i++ {
		a.resume.Skills = append(a.resume.Skills, fmt.Sprintf("Skill %d", i))
	}
	a.resume.Skills = append(a.resume.Skills, "Power BI")
	for i := 0; i < 5; i++ {
		a.resume.Experience = append(a.resume.Experience, types.Experience{Position: "Другая работа"})
	}
	a.resume.Experience = append(a.resume.Experience, types.Experience{Position: "Шестая релевантная работа"})
	job := types.Job{Title: "Analyst", URL: "https://hh.ru/vacancy/1", Snippet: strings.Repeat("Описание продукта. ", 210) + tail}
	a.jobDetails[job.URL] = job
	if _, err := a.evaluateJobForTaskWithAnalysis(job, "аналитик"); err != nil {
		t.Fatal(err)
	}
}
func TestEvaluationRejectsOversizeInsteadOfSilentlyTruncating(t *testing.T) {
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("oversize request was sent") })
	a.resume.Skills = []string{"SQL"}
	job := types.Job{Title: "Analyst", URL: "https://hh.ru/vacancy/1", Snippet: strings.Repeat("x", 65000)}
	a.jobDetails[job.URL] = job
	if _, err := a.evaluateJobForTaskWithAnalysis(job, "аналитик"); err == nil || !strings.Contains(err.Error(), "данные не обрезались") {
		t.Fatal(err)
	}
}

func TestEmptyProfileIsUnknownNotRejectedAndDoesNotCallModel(t *testing.T) {
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("empty profile must not call model") })
	a.resume = types.Resume{Name: "Кандидат", Title: "Аналитик"}
	e, err := a.evaluateJobForTaskWithAnalysis(types.Job{}, "аналитик")
	if err != nil || e.Recommendation != "review" || e.MatchScore != 1 || len(e.ReviewPoints) < 2 {
		t.Fatal(e, err)
	}
}

func TestLetterReviewRetriesInvalidSchemaAndKeepsRules(t *testing.T) {
	calls := 0
	a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req llm.GenerateRequest
		json.NewDecoder(r.Body).Decode(&req)
		if !strings.Contains(req.System, prompt.LetterReviewSystem) {
			t.Error("review rules lost during retry")
		}
		if strings.Contains(req.Prompt, "private@example.com") {
			t.Error("contacts sent to reviewer")
		}
		answer := `{"match_score":7}`
		if calls == 2 {
			answer = "```json\n{\"supported\":true,\"unsupported_claims\":[]}\n```"
		}
		json.NewEncoder(w).Encode(llm.GenerateResponse{Response: answer})
	})
	review, err := a.reviewCoverLetter(types.Resume{Skills: []string{"SQL"}, ContactInfo: types.Contact{Email: "private@example.com"}}, "Среди моих навыков SQL.")
	if err != nil || review.Supported == nil || !*review.Supported || calls != 2 {
		t.Fatal(review, err, calls)
	}
}

func TestLetterReviewBlocksInvalidAndContradictoryResponses(t *testing.T) {
	for _, body := range []string{`{}`, `{"supported":true}`, `{"supported":true,"unsupported_claims":["выдуманный стаж"]}`, `{"supported":false,"unsupported_claims":[]}`, `{"supported":"true","unsupported_claims":[]}`} {
		calls := 0
		a := mockAgent(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			json.NewEncoder(w).Encode(llm.GenerateResponse{Response: body})
		})
		if _, err := a.reviewCoverLetter(types.Resume{}, "письмо"); err == nil || calls != 2 {
			t.Fatal(body, err, calls)
		}
	}
	if review, err := parseLetterReview(`{"supported":false,"unsupported_claims":["пять лет стажа"]}`); err != nil || review.Supported == nil || *review.Supported {
		t.Fatal(review, err)
	}
}
