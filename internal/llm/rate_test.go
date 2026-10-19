package llm

import (
	"context"
	"github.com/mihnpro/Ai_agent_2/internal/workflow"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedRateScheduleAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate.json")
	t.Setenv("AI_JOB_AGENT_MODEL_REQUEST_INTERVAL_SECONDS", "12")
	t.Setenv("AI_JOB_AGENT_MODEL_RATE_FILE", path)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitModelSlot(ctx); err != nil {
		t.Fatal(err)
	}
	store := workflow.Store{Path: path}
	first, _ := store.Read()
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if err := waitModelSlot(cancelled); err != context.Canceled {
		t.Fatal(err)
	}
	second, _ := store.Read()
	if second.NextModelRequest.Sub(first.NextModelRequest) != 12*time.Second {
		t.Fatal("shared schedule did not reserve separate slots")
	}
}
