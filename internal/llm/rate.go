package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/mihnpro/Ai_agent_2/internal/workflow"
)

// The browser bridge uses one upstream account. A shared schedule also covers
// requests from the web server (mentor/import) and its browser worker process.
func waitModelSlot(ctx context.Context) error {
	configured := os.Getenv("AI_JOB_AGENT_MODEL_REQUEST_INTERVAL_SECONDS")
	if configured == "" || configured == "0" {
		return nil
	}
	interval, err := time.ParseDuration(configured + "s")
	if err != nil || interval < 0 {
		return fmt.Errorf("некорректный интервал запросов модели")
	}
	path := os.Getenv("AI_JOB_AGENT_MODEL_RATE_FILE")
	if path == "" {
		return fmt.Errorf("не указан файл очереди запросов модели")
	}
	var slot time.Time
	store := workflow.Store{Path: path}
	if err := store.Update(func(st *workflow.State) error {
		now := time.Now()
		slot = now
		if st.NextModelRequest.After(now) {
			slot = st.NextModelRequest
		}
		if slot.Sub(now) > time.Minute {
			return fmt.Errorf("модель занята; очередь запросов превышает минуту, повторите позже")
		}
		st.NextModelRequest = slot.Add(interval)
		return nil
	}); err != nil {
		return err
	}
	timer := time.NewTimer(time.Until(slot))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (c *OllamaClient) doModelRequest(req *http.Request) (*http.Response, error) {
	// A single budget includes time spent waiting for the configured rate slot.
	ctx := req.Context()
	if c.Context != nil {
		ctx = c.Context
	}
	cancel := func() {}
	if c.HTTPClient.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, c.HTTPClient.Timeout)
	}
	if c.openAICompatible() {
		if err := waitModelSlot(ctx); err != nil {
			cancel()
			return nil, err
		}
	}
	response, err := c.HTTPClient.Do(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	// Cancellation must happen after the response body has been consumed.
	response.Body = &cancelBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error { err := b.ReadCloser.Close(); b.cancel(); return err }
