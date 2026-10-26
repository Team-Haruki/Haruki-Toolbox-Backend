package story

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// llmClient calls an OpenAI-compatible /v1/chat/completions endpoint with
// streaming on (some gateways only answer streamed requests reliably).
type llmClient struct {
	baseURL string
	apiKey  string
	model   string
	timeout time.Duration
	client  *http.Client
	// retries and backoff are fields so tests can shorten them.
	retries int
	backoff time.Duration
}

type llmUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// errRetryable marks failures worth another attempt.
type errRetryable struct{ err error }

func (e errRetryable) Error() string { return e.err.Error() }
func (e errRetryable) Unwrap() error { return e.err }

// complete returns the assistant text, retrying transient failures.
func (l *llmClient) complete(ctx context.Context, system, user string) (string, llmUsage, error) {
	var last error
	for attempt := 0; attempt <= l.retries; attempt++ {
		if attempt > 0 {
			wait := l.backoff * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				return "", llmUsage{}, ctx.Err()
			case <-time.After(wait):
			}
		}
		text, usage, err := l.once(ctx, system, user)
		if err == nil {
			return text, usage, nil
		}
		last = err
		var r errRetryable
		if !errors.As(err, &r) || ctx.Err() != nil {
			break
		}
	}
	return "", llmUsage{}, last
}

func (l *llmClient) once(ctx context.Context, system, user string) (string, llmUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	body, _ := json.Marshal(map[string]any{
		"model":          l.model,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"messages":       []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(l.baseURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", llmUsage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+l.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := l.client.Do(req)
	if err != nil {
		return "", llmUsage{}, errRetryable{err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		err := fmt.Errorf("llm HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return "", llmUsage{}, errRetryable{err}
		}
		return "", llmUsage{}, err
	}
	text, usage, err := readSSE(resp.Body)
	if err != nil {
		return "", usage, errRetryable{err}
	}
	if strings.TrimSpace(text) == "" {
		return "", usage, errRetryable{errors.New("llm returned an empty completion")}
	}
	return text, usage, nil
}

// readSSE concatenates the content deltas of a chat-completions stream.
func readSSE(r io.Reader) (string, llmUsage, error) {
	var out strings.Builder
	var usage llmUsage
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	done := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *llmUsage `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			return out.String(), usage, fmt.Errorf("llm stream error: %s", chunk.Error.Message)
		}
		for _, ch := range chunk.Choices {
			out.WriteString(ch.Delta.Content)
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
	}
	if err := sc.Err(); err != nil {
		return out.String(), usage, err
	}
	if !done && out.Len() == 0 {
		return "", usage, errors.New("llm stream ended without data")
	}
	return out.String(), usage, nil
}
