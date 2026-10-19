package localui

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihnpro/Ai_agent_2/internal/types"
	"github.com/mihnpro/Ai_agent_2/internal/workflow"
)

func TestManualAPIRequiresPreviewAndPersistsQueue(t *testing.T) {
	store := &workflow.Store{Path: filepath.Join(t.TempDir(), "history.json")}
	store.Update(func(st *workflow.State) error {
		st.Entries["1"] = &workflow.Entry{ID: "1", Job: types.Job{Title: "Analyst", URL: "https://hh.ru/vacancy/1"}, Score: 7, Threshold: 6, Recommendation: "review", Status: "review", RunID: "r"}
		st.Runs["r"] = &workflow.Run{Target: 2}
		return nil
	})
	s := &server{history: store, state: taskState{Running: true}}
	action := func(body string) int {
		w := httptest.NewRecorder()
		s.handleVacancyAction(w, httptest.NewRequest("POST", "/api/vacancy/action", strings.NewReader(body)))
		return w.Code
	}
	if code := action(`{"id":"1","action":"send","version":"invented"}`); code != 409 {
		t.Fatal(code)
	}
	if code := action(`{"id":"1","action":"draft"}`); code != 202 {
		t.Fatal(code)
	}
	st, _ := store.Read()
	if len(st.Queue) != 1 || st.Queue[0].Kind != "draft" {
		t.Fatal(st)
	}
	store.Claim()
	store.Update(func(st *workflow.State) error {
		e := st.Entries["1"]
		e.Status = "draft_ready"
		e.Letter = "A verified letter"
		e.Version = "v1"
		return nil
	})
	if code := action(`{"id":"1","action":"send","version":"old"}`); code != 409 {
		t.Fatal(code)
	}
	if code := action(`{"id":"1","action":"send","version":"v1"}`); code != 202 {
		t.Fatal(code)
	}
	if code := action(`{"id":"1","action":"send","version":"v1"}`); code != 409 {
		t.Fatal("duplicate accepted", code)
	}
	if err := store.Recover(); err != nil {
		t.Fatal(err)
	}
	st, _ = store.Read()
	cards := workflowCards(st)
	if len(cards) != 1 || !cards[0].CanSend || cards[0].Letter != "A verified letter" {
		t.Fatal(cards)
	}
}
func TestHistoryShowsOnlyEligibleManualActions(t *testing.T) {
	st := workflow.State{Entries: map[string]*workflow.Entry{
		"1": {ID: "1", Score: 7, Threshold: 6, Recommendation: "review", Status: "review"},
		"2": {ID: "2", Score: 5, Threshold: 6, Recommendation: "review", Status: "review"},
		"3": {ID: "3", Score: 7, Threshold: 6, Recommendation: "review", Status: "review", ByScore: true},
		"4": {ID: "4", Status: "error", Submission: "uncertain"},
		"5": {ID: "5", Status: "error"},
	}}
	for _, card := range workflowCards(st) {
		if card.CanDraft != (card.ID == "1") {
			t.Fatal(card)
		}
		if card.CanRetry != (card.ID == "5") {
			t.Fatal(card)
		}
	}
}

func TestManualWarningAndMissingSkillsDoNotDisableConfirmation(t *testing.T) {
	st := workflow.State{Entries: map[string]*workflow.Entry{"1": {ID: "1", Status: "draft_ready", Recommendation: "review", Score: 6, Threshold: 6, Letter: "Письмо по навыкам SQL", Version: "v1", LetterWarning: "Проверьте письмо самостоятельно", ReviewPoints: []string{"Python не подтверждён"}}}}
	cards := workflowCards(st)
	if len(cards) != 1 || !cards[0].CanSend || cards[0].Details != "Проверьте письмо самостоятельно" {
		t.Fatal(cards)
	}
}
