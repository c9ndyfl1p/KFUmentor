package browser

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/types"
)

func targetJob(id int) types.Job {
	return types.Job{Title: fmt.Sprintf("Вакансия %d", id), URL: fmt.Sprintf("https://hh.ru/vacancy/%d", id)}
}
func relevantJob(types.Job) (*JobEvaluationResult, error) {
	return &JobEvaluationResult{IsRelevant: true, MatchScore: 8, Recommendation: "apply"}, nil
}

func TestApplicationTargetStopsExactlyAndUsesNextPage(t *testing.T) {
	submitted := []string{}
	searches := 0
	summary, err := runApplicationTarget(2, []string{"ML"}, applicationOperations{
		search: func(query string, page int) ([]types.Job, error) {
			searches++
			switch page {
			case 0:
				return []types.Job{targetJob(1), targetJob(2)}, nil
			case 1:
				return []types.Job{targetJob(2), targetJob(3), targetJob(4)}, nil
			}
			return nil, nil
		},
		evaluate: func(job types.Job) (*JobEvaluationResult, error) {
			if job.URL == targetJob(1).URL {
				return &JobEvaluationResult{Recommendation: "skip"}, nil
			}
			return relevantJob(job)
		},
		submit: func(job types.Job) (string, error) {
			submitted = append(submitted, job.URL)
			return "отклик подтверждён", nil
		},
	})
	if err != nil || summary.Sent != 2 || searches != 2 || len(submitted) != 2 || submitted[1] != targetJob(3).URL || summary.Scanned != 3 {
		t.Fatal(summary, submitted, searches, err)
	}
}

func TestTargetReportsShortfallAndDoesNotCountEvaluationErrors(t *testing.T) {
	summary, err := runApplicationTarget(5, []string{"ML"}, applicationOperations{
		search: func(query string, page int) ([]types.Job, error) {
			if page == 0 {
				return []types.Job{targetJob(1), targetJob(2)}, nil
			}
			return nil, nil
		},
		evaluate: func(job types.Job) (*JobEvaluationResult, error) {
			if job.URL == targetJob(1).URL {
				return nil, errors.New("model unavailable")
			}
			return relevantJob(job)
		},
		submit: func(job types.Job) (string, error) { return "подтверждён", nil },
	})
	if err != nil || summary.Sent != 1 || summary.Failures != 1 || !strings.Contains(summary.String(), "Недобор: 4") {
		t.Fatal(summary, err)
	}
}

func TestUnconfirmedApplicationStopsFurtherSubmissions(t *testing.T) {
	calls := 0
	summary, err := runApplicationTarget(3, []string{"ML"}, applicationOperations{
		search: func(string, int) ([]types.Job, error) { return []types.Job{targetJob(1), targetJob(2)}, nil }, evaluate: relevantJob,
		submit: func(types.Job) (string, error) { calls++; return "", errors.New("не подтверждено") },
	})
	if err == nil || calls != 1 || summary.Sent != 0 || summary.Failures != 1 {
		t.Fatal(summary, calls, err)
	}
}

func TestAlreadyAppliedVacancyIsSkipped(t *testing.T) {
	summary, err := runApplicationTarget(1, []string{"ML"}, applicationOperations{
		search: func(string, int) ([]types.Job, error) { return []types.Job{targetJob(1), targetJob(2)}, nil }, evaluate: relevantJob,
		submit: func(job types.Job) (string, error) {
			if job.URL == targetJob(1).URL {
				return "", errAlreadyApplied
			}
			return "подтверждён", nil
		},
	})
	if err != nil || summary.Sent != 1 || summary.Skipped != 1 || summary.Failures != 0 {
		t.Fatal(summary, err)
	}
}

func TestTargetDeduplicatesQueriesAndBoundsRepeatedPages(t *testing.T) {
	evaluations := 0
	searches := 0
	summary, err := runApplicationTarget(3, []string{"ML", "machine learning"}, applicationOperations{
		search: func(query string, page int) ([]types.Job, error) {
			searches++
			return []types.Job{targetJob(1), {Title: "Дубликат", URL: "https://kazan.hh.ru/vacancy/1?from=search"}}, nil
		},
		evaluate: func(job types.Job) (*JobEvaluationResult, error) { evaluations++; return relevantJob(job) },
		submit:   func(types.Job) (string, error) { return "подтверждён", nil },
	})
	if err != nil || summary.Sent != 1 || evaluations != 1 || searches != 4 {
		t.Fatal(summary, evaluations, searches, err)
	}
}

func TestTargetRejectsInvalidCountsWithoutSearching(t *testing.T) {
	for _, target := range []int{-1, 0, 101} {
		_, err := runApplicationTarget(target, []string{"ML"}, applicationOperations{search: func(string, int) ([]types.Job, error) { t.Fatal("invalid target started search"); return nil, nil }})
		if err == nil {
			t.Fatal(target)
		}
	}
	for _, raw := range []string{"https://example.com/vacancy/1", "https://hh.ru/vacancy/no-id", "https://hh.ru/vacancy/"} {
		if applicationJobKey(raw) != "" {
			t.Fatal(raw)
		}
	}
}

func TestDisabledAutoApplyNeverStartsTargetWorker(t *testing.T) {
	a := &AIOrchestrator{autoApply: false}
	if _, err := a.applyUntilTarget([]string{"ML"}); err == nil {
		t.Fatal("disabled auto-apply was ignored")
	}
}

func TestMaximumTargetNeverSendsAnExtraApplication(t *testing.T) {
	var jobs []types.Job
	for i := 1; i <= 101; i++ {
		jobs = append(jobs, targetJob(i))
	}
	calls := 0
	summary, err := runApplicationTarget(types.MaxApplicationTarget, []string{"ML"}, applicationOperations{
		search: func(string, int) ([]types.Job, error) { return jobs, nil }, evaluate: relevantJob,
		submit: func(types.Job) (string, error) { calls++; return "подтверждён", nil },
	})
	if err != nil || calls != 100 || summary.Sent != 100 {
		t.Fatal(summary, calls, err)
	}
}

func TestRepeatedEvaluationFailuresStopRun(t *testing.T) {
	calls := 0
	summary, err := runApplicationTarget(5, []string{"ML"}, applicationOperations{
		search: func(string, int) ([]types.Job, error) {
			return []types.Job{targetJob(1), targetJob(2), targetJob(3), targetJob(4)}, nil
		},
		evaluate: func(types.Job) (*JobEvaluationResult, error) { calls++; return nil, errors.New("model unavailable") },
		submit:   func(types.Job) (string, error) { t.Fatal("sent without evaluation"); return "", nil },
	})
	if err == nil || calls != 3 || summary.Sent != 0 || summary.Failures != 3 {
		t.Fatal(summary, calls, err)
	}
}
