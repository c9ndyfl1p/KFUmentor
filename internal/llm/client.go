package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OllamaClient struct {
	BaseURL    string
	ModelName  string
	HTTPClient *http.Client
}

type GenerateRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Stream  bool                   `json:"stream"`
	System  string                 `json:"system,omitempty"`
	Format  string                 `json:"format,omitempty"`
	Options map[string]interface{} `json:"options,omitempty"`
}

type GenerateResponse struct {
	Model    string `json:"model"`
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string                 `json:"model"`
	Messages []ChatMessage          `json:"messages"`
	Stream   bool                   `json:"stream"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

func NewOllamaClient(baseURL, modelName string) *OllamaClient {
	return &OllamaClient{
		BaseURL:   baseURL,
		ModelName: modelName,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *OllamaClient) Generate(prompt, systemPrompt string) (string, error) {
	if c.openAICompatible() {
		return c.openAIChat([]ChatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: prompt}}, false)
	}
	return c.generate(prompt, systemPrompt, "")
}

func (c *OllamaClient) GenerateJSON(prompt, systemPrompt string) (string, error) {
	if c.openAICompatible() {
		return c.openAIChat([]ChatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: prompt}}, true)
	}
	return c.generate(prompt, systemPrompt, "json")
}

func (c *OllamaClient) generate(prompt, systemPrompt, format string) (string, error) {
	reqBody := GenerateRequest{
		Model:  c.ModelName,
		Prompt: prompt,
		Stream: false,
		System: systemPrompt,
		Format: format,
		Options: map[string]interface{}{
			"temperature": 0.2,
			"top_p":       0.9,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.HTTPClient.Post(
		c.BaseURL+"/api/generate",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var genResp GenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return genResp.Response, nil
}

func (c *OllamaClient) Chat(messages []ChatMessage) (string, error) {
	if c.openAICompatible() {
		return c.openAIChat(messages, false)
	}
	reqBody := ChatRequest{
		Model:    c.ModelName,
		Messages: messages,
		Stream:   false,
		Options: map[string]interface{}{
			"temperature": 0.3,
			"top_p":       0.9,
			"num_ctx":     4096,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.HTTPClient.Post(
		c.BaseURL+"/api/chat",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var chatResp struct {
		Message ChatMessage `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return chatResp.Message.Content, nil
}

func (c *OllamaClient) openAICompatible() bool {
	return strings.HasSuffix(strings.TrimRight(c.BaseURL, "/"), "/v1")
}

func (c *OllamaClient) openAIChat(messages []ChatMessage, jsonOnly bool) (string, error) {
	request := struct {
		Model          string        `json:"model"`
		Messages       []ChatMessage `json:"messages"`
		Stream         bool          `json:"stream"`
		Temperature    float64       `json:"temperature"`
		ResponseFormat *struct {
			Type string `json:"type"`
		} `json:"response_format,omitempty"`
	}{Model: c.ModelName, Messages: messages, Stream: false, Temperature: 0.2}
	if jsonOnly {
		request.ResponseFormat = &struct {
			Type string `json:"type"`
		}{Type: "json_object"}
	}

	body, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	response, err := c.HTTPClient.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(response.Body)
		return "", fmt.Errorf("API error %d: %s", response.StatusCode, string(responseBody))
	}
	var result struct {
		Choices []struct {
			Message ChatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("API returned no chat choices")
	}
	return result.Choices[0].Message.Content, nil
}
