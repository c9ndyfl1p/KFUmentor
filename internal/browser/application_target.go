package browser

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/mihnpro/Ai_agent_2/internal/types"
)

const applicationSearchPages = 10
const applicationPageSize = 100

var errAlreadyApplied = errors.New("на эту вакансию уже отправлен отклик")

var errNoVacancies = errors.New("вакансий не найдено по данному запросу")

type applicationSummary struct {
	Target, Sent, Scanned, Evaluated, Skipped, Failures int
	Reason                                              string
	Results                                             []string
}

func (s applicationSummary) String() string {
	status := "Цель достигнута"
	if s.Sent < s.Target {
		status = fmt.Sprintf("Недобор: %d. %s", s.Target-s.Sent, s.Reason)
	}
	return fmt.Sprintf("\n📨 ОТКЛИКИ: %d из %d\nПросмотрено уникальных вакансий: %d\nОценено: %d\nПропущено: %d\nОшибок: %d\n%s\n%s", s.Sent, s.Target, s.Scanned, s.Evaluated, s.Skipped, s.Failures, status, strings.Join(s.Results, "\n"))
}

type applicationOperations struct {
	search   func(string, int) ([]types.Job, error)
	evaluate func(types.Job) (*JobEvaluationResult, error)
	submit   func(types.Job) (string, error)
	progress func(int, int)
}

// Count successful applications, rather than vacancies inspected or attempts.
// New search pages replenish the pool when jobs do not fit the saved profile.
func runApplicationTarget(target int, queries []string, ops applicationOperations) (applicationSummary, error) {
	summary := applicationSummary{Target: target}
	if target < 1 || target > types.MaxApplicationTarget {
		return summary, fmt.Errorf("количество откликов должно быть от 1 до %d", types.MaxApplicationTarget)
	}
	if len(queries) == 0 {
		return summary, fmt.Errorf("нет поисковых запросов")
	}
	seen := map[string]bool{}
	consecutiveEvaluationErrors := 0
	finished := make([]bool, len(queries))
	pageSignatures := make([]map[string]bool, len(queries))
	for i := range queries {
		pageSignatures[i] = map[string]bool{}
	}
	for page := 0; page < applicationSearchPages; page++ {
		active := false
		for index, query := range queries {
			if finished[index] {
				continue
			}
			active = true
			jobs, err := ops.search(query, page)
			if err != nil {
				summary.Reason = "Поиск остановлен из-за ошибки загрузки результатов."
				return summary, err
			}
			if len(jobs) == 0 {
				finished[index] = true
				continue
			}
			var ids []string
			for _, job := range jobs {
				ids = append(ids, applicationJobKey(job.URL))
			}
			sort.Strings(ids)
			signature := strings.Join(ids, "|")
			if pageSignatures[index][signature] {
				finished[index] = true
				continue
			}
			pageSignatures[index][signature] = true
			for _, job := range jobs {
				key := applicationJobKey(job.URL)
				if key == "" || seen[key] {
					continue
				}
				seen[key] = true
				summary.Scanned++
				evaluation, err := ops.evaluate(job)
				if err != nil {
					summary.Failures++
					consecutiveEvaluationErrors++
					summary.Results = append(summary.Results, fmt.Sprintf("❌ %s: ошибка оценки", job.Title))
					if consecutiveEvaluationErrors >= 3 {
						summary.Reason = "Три ошибки оценки подряд: проверьте модель и доступность описаний вакансий."
						return summary, err
					}
					continue
				}
				consecutiveEvaluationErrors = 0
				summary.Evaluated++
				if evaluation == nil || !evaluation.IsRelevant || evaluation.MatchScore < 7 || evaluation.Recommendation != "apply" {
					summary.Skipped++
					continue
				}
				result, err := ops.submit(job)
				if errors.Is(err, errAlreadyApplied) {
					summary.Skipped++
					continue
				}
				if err != nil {
					summary.Failures++
					summary.Reason = "Отправка остановлена: результат последнего отклика не подтверждён. Проверьте «Мои отклики» на hh.ru перед повторным запуском."
					summary.Results = append(summary.Results, fmt.Sprintf("❌ %s: %v", job.Title, err))
					return summary, err
				}
				summary.Sent++
				summary.Results = append(summary.Results, fmt.Sprintf("✅ %s: %s", job.Title, result))
				if ops.progress != nil {
					ops.progress(summary.Sent, target)
				}
				if summary.Sent == target {
					return summary, nil
				}
			}
		}
		if !active {
			break
		}
	}
	summary.Reason = "Подходящих вакансий в просмотренных результатах оказалось меньше цели. Проверено до 10 страниц на каждый поисковый запрос."
	return summary, nil
}

func applicationJobKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if (u.Hostname() != "hh.ru" && !strings.HasSuffix(u.Hostname(), ".hh.ru")) || !strings.HasPrefix(u.Path, "/vacancy/") {
		return ""
	}
	id := strings.Trim(strings.TrimPrefix(u.Path, "/vacancy/"), "/")
	parsed, err := strconv.ParseUint(id, 10, 64)
	if err != nil || parsed == 0 || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return "/vacancy/" + strconv.FormatUint(parsed, 10)
}

func (a *AIOrchestrator) applyUntilTarget(queries []string) (string, error) {
	if !a.autoApply {
		return "", fmt.Errorf("отправка откликов не включена")
	}
	target := a.applicationTarget
	if target == 0 {
		target = types.DefaultApplicationTarget
	}
	log.Printf("🎯 Желаемое количество откликов: %d", target)
	summary, err := runApplicationTarget(target, queries, applicationOperations{
		search: func(query string, page int) ([]types.Job, error) {
			log.Printf("🔍 Ищу вакансии: %s, страница %d", query, page+1)
			if _, err := a.searchJobsPage(query, page); err != nil {
				return nil, err
			}
			jobs, err := a.extractJobsFromPage(applicationPageSize)
			if errors.Is(err, errNoVacancies) {
				return nil, nil
			}
			return jobs, err
		},
		evaluate: func(job types.Job) (*JobEvaluationResult, error) {
			evaluation, err := a.evaluateJobForTaskWithAnalysis(job, a.currentTask)
			if err == nil {
				a.printJobAnalysis(job, evaluation)
			}
			return evaluation, err
		},
		submit: a.performJobApplication,
		progress: func(sent, target int) {
			log.Printf("📨 Отправлено откликов: %d из %d", sent, target)
		},
	})
	log.Print(summary.String())
	return summary.String(), err
}
