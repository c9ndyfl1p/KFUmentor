package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GenerateStructured is cancellable so closing an import does not keep a model
// request running. Native Ollama constrains the response to the supplied schema.
func (c *OllamaClient) GenerateStructured(ctx context.Context, prompt, system string, schema json.RawMessage) (string, error) {
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/api/generate"
	var payload interface{} = struct {
		Model   string                 `json:"model"`
		Prompt  string                 `json:"prompt"`
		System  string                 `json:"system"`
		Format  json.RawMessage        `json:"format"`
		Stream  bool                   `json:"stream"`
		Think   bool                   `json:"think"`
		Options map[string]interface{} `json:"options"`
	}{c.ModelName, prompt, system, schema, false, false, map[string]interface{}{"temperature": 0, "num_ctx": 16384, "num_predict": 6000}}
	if c.openAICompatible() {
		endpoint = strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
		payload = map[string]interface{}{
			"model": c.ModelName, "messages": []ChatMessage{{Role: "system", Content: system}, {Role: "user", Content: prompt}},
			"stream": false, "temperature": 0, "max_tokens": 6000,
			"response_format": map[string]interface{}{"type": "json_schema", "json_schema": map[string]interface{}{"name": "resume_import", "strict": true, "schema": schema}},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.openAICompatible() && c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.doModelRequest(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("сервер модели вернул HTTP %d", resp.StatusCode)
	}
	var result struct {
		Response   string `json:"response"`
		DoneReason string `json:"done_reason"`
		Choices    []struct {
			Message      ChatMessage `json:"message"`
			FinishReason string      `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", err
	}
	if c.openAICompatible() {
		if len(result.Choices) == 0 {
			return "", fmt.Errorf("модель не вернула ответ")
		}
		if result.Choices[0].FinishReason == "length" {
			return "", fmt.Errorf("ответ модели обрезан; сократите резюме")
		}
		return result.Choices[0].Message.Content, nil
	}
	if result.DoneReason == "length" {
		return "", fmt.Errorf("ответ модели обрезан; сократите резюме")
	}
	return result.Response, nil
}
