package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type OllamaClient struct {
	BaseURL    string
	ModelName  string
	APIKey     string
	HTTPClient *http.Client
}

type GenerateRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Stream  bool                   `json:"stream"`
	Think   bool                   `json:"think"`
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
	timeout := 180 * time.Second
	if configured := strings.TrimSpace(os.Getenv("AI_JOB_AGENT_MODEL_TIMEOUT_SECONDS")); configured != "" {
		if seconds, err := time.ParseDuration(configured + "s"); err == nil && seconds > 0 {
			timeout = seconds
		}
	}
	return &OllamaClient{
		BaseURL:   baseURL,
		ModelName: modelName,
		APIKey:    os.Getenv("AI_JOB_AGENT_MODEL_API_KEY"),
		HTTPClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *OllamaClient) Generate(prompt, systemPrompt string) (string, error) {
	if c.openAICompatible() {
		return c.openAIChat([]ChatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: prompt}}, false)
	}
	return c.generate(prompt, systemPrompt, "")
}

// GenerateWithBudget bounds a short generation call so slow local models do
// not block the job workflow for the client's full default timeout.
func (c *OllamaClient) GenerateWithBudget(prompt, systemPrompt string, maxTokens int, timeout time.Duration) (string, error) {
	client := *c.HTTPClient
	if timeout > 0 {
		client.Timeout = timeout
	}
	bounded := *c
	bounded.HTTPClient = &client
	if bounded.openAICompatible() {
		return bounded.openAIChatWithMaxTokens([]ChatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: prompt}}, false, maxTokens)
	}
	return bounded.generateWithMaxTokens(prompt, systemPrompt, "", maxTokens)
}

func (c *OllamaClient) GenerateJSON(prompt, systemPrompt string) (string, error) {
	if c.openAICompatible() {
		return c.openAIChat([]ChatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: prompt}}, true)
	}
	return c.generate(prompt, systemPrompt, "json")
}

// GenerateJSONWithBudget keeps structured-output mode enabled while bounding
// response length and request duration for slower local models.
func (c *OllamaClient) GenerateJSONWithBudget(prompt, systemPrompt string, maxTokens int, timeout time.Duration) (string, error) {
	client := *c.HTTPClient
	if timeout > 0 {
		client.Timeout = timeout
	}
	bounded := *c
	bounded.HTTPClient = &client
	if bounded.openAICompatible() {
		return bounded.openAIChatWithMaxTokens([]ChatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: prompt}}, true, maxTokens)
	}
	return bounded.generateWithMaxTokens(prompt, systemPrompt, "json", maxTokens)
}

func (c *OllamaClient) generate(prompt, systemPrompt, format string) (string, error) {
	return c.generateWithMaxTokens(prompt, systemPrompt, format, 0)
}

func (c *OllamaClient) generateWithMaxTokens(prompt, systemPrompt, format string, maxTokens int) (string, error) {
	options := map[string]interface{}{
		"temperature": 0.2,
		"top_p":       0.9,
		"num_ctx":     8192,
	}
	if maxTokens > 0 {
		options["num_predict"] = maxTokens
	}
	reqBody := GenerateRequest{
		Model:   c.ModelName,
		Prompt:  prompt,
		Stream:  false,
		System:  systemPrompt,
		Format:  format,
		Options: options,
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
	return c.openAIChatWithMaxTokens(messages, jsonOnly, 0)
}

func (c *OllamaClient) openAIChatWithMaxTokens(messages []ChatMessage, jsonOnly bool, maxTokens int) (string, error) {
	request := struct {
		Model          string        `json:"model"`
		Messages       []ChatMessage `json:"messages"`
		Stream         bool          `json:"stream"`
		Temperature    float64       `json:"temperature"`
		MaxTokens      int           `json:"max_tokens,omitempty"`
		ResponseFormat *struct {
			Type string `json:"type"`
		} `json:"response_format,omitempty"`
	}{Model: c.ModelName, Messages: messages, Stream: false, Temperature: 0.2, MaxTokens: maxTokens}
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
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	response, err := c.doModelRequest(req)
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
