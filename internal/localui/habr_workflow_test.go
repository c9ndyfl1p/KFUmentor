package localui

import (
	"context"
	"fmt"
	"github.com/mihnpro/Ai_agent_2/internal/browser"
	"github.com/mihnpro/Ai_agent_2/internal/habr"
	"github.com/mihnpro/Ai_agent_2/internal/types"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func waitHabr(t *testing.T, s *server) habrState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.habr.mu.Lock()
		state := s.habr.state
		s.habr.mu.Unlock()
		if !state.Running {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Habr job did not finish")
	return habrState{}
}
func TestHabrQualifiedLimitAndRetry(t *testing.T) {
	t.Chdir(t.TempDir())
	s := &server{}
	s.habr.resolve = func(context.Context, string) []string { return []string{"SQL"} }
	s.habr.collect = func(ctx context.Context, raw string, limit int, emit func(habr.Job)) error {
		for i := 1; i <= 5; i++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			emit(habr.Job{URL: fmt.Sprintf("https://career.habr.com/vacancies/%d", i), Title: "Analyst", Description: "SQL"})
		}
		return nil
	}
	s.habr.evaluate = func(ctx context.Context, _ types.Resume, j habr.Job, task, area string) (*browser.JobEvaluationResult, error) {
		score := 8
		if strings.HasSuffix(j.URL, "/1") {
			score = 3
		}
		return &browser.JobEvaluationResult{MatchScore: score, Recommendation: "review", Reason: "Test"}, nil
	}
	w := httptest.NewRecorder()
	s.handleHabr(w, httptest.NewRequest("POST", "/api/habr", strings.NewReader(`{"task":"SQL analyst","limit":2,"minimumScore":6}`)))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	state := waitHabr(t, s)
	if len(state.Jobs) != 3 || state.Jobs[0].Score != 3 || !strings.Contains(state.Status, "2 из 2") {
		t.Fatalf("wrong target: %+v", state)
	}
	w = httptest.NewRecorder()
	s.handleHabr(w, httptest.NewRequest("POST", "/api/habr", strings.NewReader(`{"retryURL":"https://evil.test/private"}`)))
	if w.Code != 404 {
		t.Fatal("accepted unknown retry", w.Code)
	}
	// A retry replaces the existing URL, it must not duplicate its card.
	s.habr.collect = func(ctx context.Context, raw string, limit int, emit func(habr.Job)) error {
		emit(habr.Job{URL: raw, Title: "Retried", Description: "SQL"})
		return nil
	}
	w = httptest.NewRecorder()
	s.handleHabr(w, httptest.NewRequest("POST", "/api/habr", strings.NewReader(`{"retryURL":"https://career.habr.com/vacancies/1"}`)))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	state = waitHabr(t, s)
	if len(state.Jobs) != 3 || state.Jobs[0].Title != "Retried" {
		t.Fatal("retry lost or duplicated history", state)
	}
}
func TestHabrAreaFilter(t *testing.T) {
	for _, tc := range []struct {
		job  habr.Job
		want bool
	}{
		{habr.Job{Locations: []string{"Казань"}}, true},
		{habr.Job{Locations: []string{"Москва"}}, false},
		{habr.Job{Conditions: []string{"Можно удалённо"}}, true},
		{habr.Job{}, false},
	} {
		if got := habrMatchesArea(tc.job, "Казань"); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
func TestHabrCancellationDuringEvaluation(t *testing.T) {
	t.Chdir(t.TempDir())
	s := &server{}
	entered := make(chan struct{})
	s.habr.resolve = func(context.Context, string) []string { return []string{"SQL"} }
	s.habr.collect = func(ctx context.Context, _ string, _ int, emit func(habr.Job)) error {
		emit(habr.Job{URL: "https://career.habr.com/vacancies/1", Description: "SQL"})
		return ctx.Err()
	}
	s.habr.evaluate = func(ctx context.Context, _ types.Resume, _ habr.Job, _, _ string) (*browser.JobEvaluationResult, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	w := httptest.NewRecorder()
	s.handleHabr(w, httptest.NewRequest("POST", "/api/habr", strings.NewReader(`{"task":"SQL","limit":1}`)))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("evaluation never started")
	}
	s.handleHabr(httptest.NewRecorder(), httptest.NewRequest("DELETE", "/api/habr", nil))
	state := waitHabr(t, s)
	if state.Jobs[0].Status != "stopped" {
		t.Fatal(state)
	}
}
