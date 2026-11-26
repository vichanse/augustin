package openai

import (
	"strings"
	"testing"
)

func TestDecodeStream(t *testing.T) {
	input := "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	var text strings.Builder
	result, err := decodeStream(strings.NewReader(input), func(s string) { text.WriteString(s) })
	if err != nil || result.Message.Content != "hello world" || text.String() != "hello world" {
		t.Fatalf("unexpected stream result: %v", err)
	}
	if _, err := decodeStream(strings.NewReader(strings.Split(input, "data: [DONE]")[0]), func(string) {}); err == nil {
		t.Fatal("incomplete stream accepted")
	}
}
