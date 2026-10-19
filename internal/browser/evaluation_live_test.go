package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mihnpro/Ai_agent_2/internal/llm"
	"github.com/mihnpro/Ai_agent_2/internal/types"
)

type evaluationCase struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	ProfileVariant string `json:"profileVariant,omitempty"`
	Min            int    `json:"min"`
	Max            int    `json:"max"`
	Recommendation string `json:"recommendation"`
}
type evaluationObservation struct {
	Case       evaluationCase       `json:"case"`
	Repeat     int                  `json:"repeat"`
	Evaluation *JobEvaluationResult `json:"evaluation,omitempty"`
	Error      string               `json:"error,omitempty"`
	Seconds    float64              `json:"seconds"`
	Passed     bool                 `json:"passed"`
}

func TestDeepSeekEvaluationBenchmark(t *testing.T) {
	if os.Getenv("KFUMENTOR_DEEPSEEK_BENCHMARK") != "1" {
		t.Skip("opt-in real model benchmark; synthetic profiles only; no hh.ru or applications")
	}
	data, err := os.ReadFile("testdata/evaluation_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []evaluationCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	observations := []evaluationObservation{}
	output := os.Getenv("KFUMENTOR_BENCHMARK_OUTPUT")
	if output == "" {
		t.Fatal("set benchmark output path")
	}
	repeats := 2
	if configured := os.Getenv("KFUMENTOR_BENCHMARK_REPEATS"); configured != "" {
		repeats, _ = strconv.Atoi(configured)
	}
	for repeat := 1; repeat <= repeats; repeat++ {
		for _, tc := range cases {
			if filter := os.Getenv("KFUMENTOR_BENCHMARK_FILTER"); filter != "" && !strings.Contains(","+filter+",", ","+tc.ID+",") {
				continue
			}
			profile := types.Resume{Name: "Тестовый кандидат", Title: "Junior бизнес-аналитик", Summary: "Студент 3 курса бакалавриата по бизнес-информатике, диплом ещё не получен. Коммерческого опыта нет. Выполнял учебные проекты по сбору требований, CRM и дашбордам. Ищу только удалённую работу без переезда.", Skills: []string{"SQL: SELECT, JOIN, GROUP BY", "Excel", "Power Query", "Python: базовый уровень", "сбор и формализация требований", "BPMN", "Figma", "REST API"}, Projects: []types.Project{{Name: "Учебная CRM", Description: "Описал требования, пользовательские сценарии и сущности CRM, создал прототипы в Figma. Некоммерческий учебный проект."}, {Name: "Учебный дашборд", Description: "Подготовил отчёт по продажам с SQL и Excel. Учебный проект."}}, Education: []types.Education{{Degree: "Бакалавриат, 3 курс, не окончен", University: "Тестовый университет", Year: "2027 (ожидается)"}}}
			switch tc.ProfileVariant {
			case "empty":
				profile = types.Resume{}
			case "unknown_education":
				profile.Education = nil
				profile.Summary = "Junior бизнес-аналитик. Выполнил учебные проекты по требованиям, CRM и отчётности. Коммерческого опыта нет. Только удалённая работа."
			case "late_skills":
				profile.Skills = nil
				for i := 0; i < 30; i++ {
					profile.Skills = append(profile.Skills, fmt.Sprintf("Тестовый инструмент %d", i))
				}
				profile.Skills = append(profile.Skills, "Power BI", "DAX", "SQL", "Excel")
			case "achievement":
				profile.Achievements = []types.Achievement{{Name: "Студенческий конкурс по аналитике", Result: "1 место", Date: "2025"}}
			}
			job := types.Job{Title: tc.Title, Company: "Тестовый работодатель", URL: "https://hh.ru/vacancy/1", Snippet: tc.Description}
			client := llm.NewOllamaClient("http://127.0.0.1:8000/v1", "deepseek-chat")
			a := &AIOrchestrator{llmClient: client, resume: profile, jobDetails: map[string]types.Job{job.URL: job}, letterExamplesPath: filepath.Join(t.TempDir(), "missing.json"), minimumScore: 6}
			started := time.Now()
			evaluation, evalErr := a.evaluateJobForTaskWithAnalysis(job, "Найди вакансии junior бизнес-аналитика. Только удалённая работа, без переезда.")
			observation := evaluationObservation{Case: tc, Repeat: repeat, Evaluation: evaluation, Seconds: time.Since(started).Seconds()}
			if evalErr != nil {
				observation.Error = evalErr.Error()
			} else {
				observation.Passed = evaluation.MatchScore >= tc.Min && evaluation.MatchScore <= tc.Max && evaluation.Recommendation == tc.Recommendation
			}
			observations = append(observations, observation)
			b, _ := json.MarshalIndent(observations, "", "  ")
			if err := os.WriteFile(output, b, 0600); err != nil {
				t.Fatal(err)
			}
			if evalErr != nil {
				t.Fatalf("%s #%d ERROR; benchmark stopped to avoid repeated failed requests: %v", tc.ID, repeat, evalErr)
			} else {
				t.Logf("%s #%d score=%d rec=%s expected=%d..%d/%s pass=%v %.1fs", tc.ID, repeat, evaluation.MatchScore, evaluation.Recommendation, tc.Min, tc.Max, tc.Recommendation, observation.Passed, observation.Seconds)
			}
		}
	}
	missed := 0
	for _, observation := range observations {
		if !observation.Passed {
			missed++
		}
	}
	t.Logf("Control expectations met: %d/%d", len(observations)-missed, len(observations))
	if missed > 0 {
		t.Errorf("%d control expectations were not met; see saved observations", missed)
	}

}
