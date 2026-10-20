package localui

import (
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
