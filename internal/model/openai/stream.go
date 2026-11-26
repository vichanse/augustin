package openai

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/vichanse/augustin/internal/model"
)

func decodeStream(r io.Reader, delta func(string)) (model.Response, error) {
	var result model.Response
	var content strings.Builder
	var calls []tool
	scanner := bufio.NewScanner(io.LimitReader(r, maxBody+1))
	scanner.Buffer(make([]byte, 4096), maxBody)
	done := false
	for scanner.Scan() {
		line := scanner.Text()
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
				Index int `json:"index"`
				Delta struct {
					Role      model.Role `json:"role"`
					Content   string     `json:"content"`
					ToolCalls []struct {
						Index int `json:"index"`
						tool
					} `json:"tool_calls"`
				} `json:"delta"`
				Finish string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				Input  int `json:"prompt_tokens"`
				Output int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) > 1 {
			return model.Response{}, errors.New("invalid OpenAI stream chunk")
		}
		if chunk.Usage != nil {
			result.Usage = model.Usage{InputTokens: chunk.Usage.Input, OutputTokens: chunk.Usage.Output}
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				return model.Response{}, errors.New("invalid OpenAI stream choice")
			}
			d := choice.Delta
			if d.Role != "" {
				result.Message.Role = d.Role
			}
			if d.Content != "" {
				content.WriteString(d.Content)
				delta(d.Content)
			}
			for _, t := range d.ToolCalls {
				if t.Index < 0 || t.Index >= 128 || t.Index > len(calls) {
					return model.Response{}, errors.New("invalid OpenAI stream tool index")
				}
				if t.Index == len(calls) {
					calls = append(calls, tool{})
				}
				c := &calls[t.Index]
				c.ID += t.ID
				if t.Type != "" {
					c.Type = t.Type
				}
				c.Function.Name += t.Function.Name
				c.Function.Arguments += t.Function.Arguments
			}
			if choice.Finish != "" {
				result.FinishReason = model.FinishReason(choice.Finish)
			}
		}
	}
	if scanner.Err() != nil || !done || result.FinishReason == "" || result.Message.Role != model.RoleAssistant {
		return model.Response{}, errors.New("incomplete OpenAI stream")
	}
	result.Message.Content = content.String()
	for _, t := range calls {
		if t.Type != "function" || !json.Valid([]byte(t.Function.Arguments)) {
			return model.Response{}, errors.New("invalid OpenAI stream tool call")
		}
		result.Message.ToolCalls = append(result.Message.ToolCalls, model.ToolCall{ID: t.ID, Name: t.Function.Name, Arguments: json.RawMessage(t.Function.Arguments)})
	}
	return result, nil
}
