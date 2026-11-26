// Package openai implements the OpenAI Chat Completions adapter.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/vichanse/augustin/internal/model"
)

const endpoint = "https://api.openai.com/v1/chat/completions"
const maxBody = 4 << 20

type Provider struct {
	key, name string
	maxTokens int
	client    *http.Client
	url       string
}

var _ model.Provider = (*Provider)(nil)

func New(key, name string) (*Provider, error) {
	if key == "" || name == "" {
		return nil, errors.New("API key and model required")
	}
	maxTokens := 8192
	if value := os.Getenv("AUGUSTIN_MAX_COMPLETION_TOKENS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1024 || n > 32768 {
			return nil, errors.New("invalid completion token limit")
		}
		maxTokens = n
	}
	// Client.Timeout would also cap streamed body reads; the caller's ctx bounds total duration.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 60 * time.Second
	return &Provider{key: key, name: name, maxTokens: maxTokens, url: endpoint, client: &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}, nil
}

type function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
}

type tool struct {
	ID       string   `json:"id,omitempty"`
	Type     string   `json:"type"`
	Function function `json:"function"`
}

type message struct {
	Role       model.Role `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []tool     `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

func (p *Provider) Complete(ctx context.Context, req model.Request) (model.Response, error) {
	return p.complete(ctx, req, nil)
}

func (p *Provider) CompleteStream(ctx context.Context, req model.Request, delta func(string)) (model.Response, error) {
	return p.complete(ctx, req, delta)
}

func (p *Provider) complete(ctx context.Context, req model.Request, delta func(string)) (model.Response, error) {
	var result model.Response
	payload := struct {
		Model               string    `json:"model"`
		Messages            []message `json:"messages"`
		Tools               []tool    `json:"tools,omitempty"`
		MaxCompletionTokens int       `json:"max_completion_tokens"`
		Store               bool      `json:"store"`
		Stream              bool      `json:"stream,omitempty"`
		StreamOptions       any       `json:"stream_options,omitempty"`
	}{Model: p.name, MaxCompletionTokens: p.maxTokens}
	if payload.MaxCompletionTokens == 0 {
		payload.MaxCompletionTokens = 8192
	}
	if delta != nil {
		payload.Stream = true
		payload.StreamOptions = map[string]bool{"include_usage": true}
	}
	for _, m := range req.Messages {
		wire := message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, c := range m.ToolCalls {
			wire.ToolCalls = append(wire.ToolCalls, tool{ID: c.ID, Type: "function", Function: function{Name: c.Name, Arguments: string(c.Arguments)}})
		}
		payload.Messages = append(payload.Messages, wire)
	}
	for _, t := range req.Tools {
		payload.Tools = append(payload.Tools, tool{Type: "function", Function: function{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}})
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > maxBody {
		return result, errors.New("invalid or oversized OpenAI request")
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return result, errors.New("invalid OpenAI HTTP request")
	}
	r.Header.Set("Authorization", "Bearer "+p.key)
	r.Header.Set("Content-Type", "application/json")
	res, err := p.client.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, errors.New("OpenAI transport failure")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return result, fmt.Errorf("OpenAI HTTP status %d", res.StatusCode)
	}
	if delta != nil {
		return decodeStream(res.Body, delta)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return result, errors.New("invalid or oversized OpenAI response")
	}
	var decoded struct {
		Choices []struct {
			Message message `json:"message"`
			Finish  string  `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			Input  int `json:"prompt_tokens"`
			Output int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &decoded) != nil || len(decoded.Choices) != 1 {
		return result, errors.New("invalid OpenAI completion")
	}
	c := decoded.Choices[0]
	result.Message = model.Message{Role: c.Message.Role, Content: c.Message.Content}
	for _, t := range c.Message.ToolCalls {
		if t.Type != "function" || !json.Valid([]byte(t.Function.Arguments)) {
			return model.Response{}, errors.New("invalid OpenAI tool call")
		}
		result.Message.ToolCalls = append(result.Message.ToolCalls, model.ToolCall{ID: t.ID, Name: t.Function.Name, Arguments: json.RawMessage(t.Function.Arguments)})
	}
	switch c.Finish {
	case "stop", "tool_calls", "length":
		result.FinishReason = model.FinishReason(c.Finish)
	default:
		result.FinishReason = model.FinishOther
	}
	result.Usage = model.Usage{InputTokens: decoded.Usage.Input, OutputTokens: decoded.Usage.Output}
	return result, nil
}
