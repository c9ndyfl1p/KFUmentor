package localui

import (
	"encoding/json"
	"github.com/mihnpro/Ai_agent_2/internal/habr"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHabrHistoryAndValidation(t *testing.T) {
	t.Chdir(t.TempDir())
	h := habrService{state: habrState{Running: true, URL: "https://career.habr.com/vacancies", Limit: 2, Jobs: []habr.Job{{Source: "habr", URL: "https://career.habr.com/vacancies/123", Title: "Analyst"}}}}
	if err := h.save(); err != nil {
		t.Fatal(err)
	}
	s := &server{}
	w := httptest.NewRecorder()
	s.handleHabr(w, httptest.NewRequest("GET", "/api/habr", nil))
	var state habrState
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Running || len(state.Jobs) != 1 || !strings.Contains(state.Status, "прерван") {
		t.Fatalf("recovery: %+v", state)
	}
	for _, body := range []string{`{"url":"http://localhost/private","limit":2}`, `{"url":"https://career.habr.com/vacancies","limit":101}`} {
		w = httptest.NewRecorder()
		s.handleHabr(w, httptest.NewRequest("POST", "/api/habr", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("accepted invalid input: %d", w.Code)
		}
		if len(s.habr.state.Jobs) != 1 {
			t.Fatal("validation replaced history")
		}
	}
	s.habr.state.Running = true
	w = httptest.NewRecorder()
	s.handleHabr(w, httptest.NewRequest("POST", "/api/habr", strings.NewReader(`{}`)))
	if w.Code != 409 {
		t.Fatal("parallel job accepted")
	}
	cancelled := false
	s.habr.cancel = func() { cancelled = true }
	w = httptest.NewRecorder()
	s.handleHabr(w, httptest.NewRequest("DELETE", "/api/habr", nil))
	if !cancelled || len(s.habr.state.Jobs) != 1 {
		t.Fatal("cancel lost results")
	}
}
