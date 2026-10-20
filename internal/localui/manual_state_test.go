package localui

import (
	"github.com/mihnpro/Ai_agent_2/internal/types"
	"github.com/mihnpro/Ai_agent_2/internal/workflow"
	"testing"
	"time"
)

func TestManualTaskPreservesSearchState(t *testing.T) {
	started := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	s := &server{state: taskState{MinimumScore: 6, Output: "original search log\n", StartedAt: &started, Cards: []vacancyCard{{Title: "Original"}}}}
	if !s.beginTask("Manual", true) {
		t.Fatal("not started")
	}
	if s.state.MinimumScore != 6 || s.state.Output != "original search log\n" || !s.state.StartedAt.Equal(started) || len(s.state.Cards) != 1 {
		t.Fatalf("search state lost: %+v", s.state)
	}
	if s.beginTask("Duplicate", true) {
		t.Fatal("parallel worker accepted")
	}
	s.state.Running = false
	if !s.beginTask("New search") {
		t.Fatal("not started")
	}
	if s.state.Output != "" || s.state.StartedAt.Equal(started) {
		t.Fatal("new search must reset state")
	}
}

func TestQuestionnaireCardsPreserveScoreAndBlockSend(t *testing.T) {
	st := workflow.State{Entries: map[string]*workflow.Entry{"1": {ID: "1", Job: types.Job{Title: "Driver", URL: "https://hh.ru/vacancy/1", ApplicationMode: "questionnaire", ApplicationURL: "https://hh.ru/article/31727", DescriptionStatus: "ready"}, Status: "assessed", Score: 8, Recommendation: "apply"}}}
	cards := workflowCards(st)
	if len(cards) != 1 || cards[0].Status != "questionnaire" || cards[0].CanDraft || cards[0].CanSend || cards[0].CanRetry || cards[0].Score != 8 || cards[0].ApplicationURL != "https://hh.ru/article/31727" {
		t.Fatal(cards)
	}
	st.Entries["1"].Job.ApplicationURL = "javascript:alert(1)"
	if c := workflowCards(st)[0]; c.ApplicationURL != "https://hh.ru/vacancy/1" {
		t.Fatal("unsafe URL", c)
	}
}
