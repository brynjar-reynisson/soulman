package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Summarizer produces a one-line summary of text. *DeepSeekClient is the
// production implementation; tests inject a fake to exercise the fallback
// paths in rules.ErrorReportRule without a network call or a real
// DEEPSEEK_API_KEY.
type Summarizer interface {
	Summarize(ctx context.Context, text string) (string, error)
}

const systemPrompt = "Summarize this error in one line, under 120 characters, plain text, no markdown."

// DeepSeekClient calls the DeepSeek Chat Completions API
// (https://api.deepseek.com/chat/completions, OpenAI-compatible).
type DeepSeekClient struct {
	apiKey     string
	baseURL    string
	model      string
	timeout    time.Duration
	httpClient *http.Client
}

func NewDeepSeekClient(apiKey, baseURL, model string, timeout time.Duration) *DeepSeekClient {
	return &DeepSeekClient{
		apiKey:     apiKey,
		baseURL:    baseURL,
		model:      model,
		timeout:    timeout,
		httpClient: &http.Client{},
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// Summarize sends a single non-streaming Chat Completions request. Callers
// are responsible for truncating text before calling (thinking-svc's design
// spec truncates to 4000 characters before summarization; the full text
// still travels through in the Action Request separately).
func (c *DeepSeekClient) Summarize(ctx context.Context, text string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	reqBody, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: text},
		},
		Stream: false,
	})
	if err != nil {
		return "", fmt.Errorf("deepseek: marshal request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("deepseek: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("deepseek: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("deepseek: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("deepseek: status %d: %s", resp.StatusCode, string(body))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("deepseek: unmarshal response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("deepseek: empty choices in response")
	}

	return parsed.Choices[0].Message.Content, nil
}

// classifyResponse is the expected shape of the classifier's JSON response
// content (parsed from chatResponse.Choices[0].Message.Content).
type classifyResponse struct {
	Important bool   `json:"important"`
	Reason    string `json:"reason"`
}

// ClassifyImportance sends a single non-streaming Chat Completions request
// asking whether an email is important. Unlike Summarize, this method
// never returns a non-nil error: any failure (network, non-200, malformed
// response) is converted into (false, "classification unavailable: ...")
// instead — a fail-closed default so an LLM hiccup never triggers a
// spurious Discord notification, while the caller (GmailTriageRule) still
// gets a reason string worth logging to the daily report.
func (c *DeepSeekClient) ClassifyImportance(ctx context.Context, sender, subject, body string) (bool, string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	userMsg := fmt.Sprintf("From: %s\nSubject: %s\n\n%s", sender, subject, body)

	reqBody, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: classifierSystemPrompt},
			{Role: "user", Content: userMsg},
		},
		Stream: false,
	})
	if err != nil {
		return false, fmt.Sprintf("classification unavailable: marshal request: %v", err), nil
	}

	url := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return false, fmt.Sprintf("classification unavailable: build request: %v", err), nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return false, fmt.Sprintf("classification unavailable: request failed: %v", err), nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Sprintf("classification unavailable: read response: %v", err), nil
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("classification unavailable: deepseek status %d", resp.StatusCode), nil
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil || len(parsed.Choices) == 0 {
		return false, "classification unavailable: empty or malformed deepseek response", nil
	}

	var result classifyResponse
	if err := json.Unmarshal([]byte(parsed.Choices[0].Message.Content), &result); err != nil {
		return false, "classification unavailable: non-JSON classifier response", nil
	}

	return result.Important, result.Reason, nil
}
