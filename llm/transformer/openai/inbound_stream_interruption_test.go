package openai

import (
	"context"
	"strings"
	"testing"
)

// TestInboundTransformer_CompletionEvents verifies the synthesized termination
// events match the OpenAI Chat Completions streaming protocol:
//
//	data: {"id":...,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}
//	data: [DONE]
//
// Per the OpenAI streaming docs: the final content chunk carries
// choices[].finish_reason (object: chat.completion.chunk), then the stream
// terminates with "data: [DONE]". finish_reason:"length" is the honest mapping
// for a mid-stream drop (capped/truncated), distinct from "stop" (model done).
func TestInboundTransformer_CompletionEvents(t *testing.T) {
	tt := &InboundTransformer{}
	events := tt.CompletionEvents(context.Background())
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	// First event: the terminal chunk with finish_reason and the chunk object.
	first := string(events[0].Data)
	if !strings.Contains(first, `"object":"chat.completion.chunk"`) {
		t.Fatalf("first event = %s, want object:chat.completion.chunk", first)
	}
	if !strings.Contains(first, `"finish_reason":"length"`) {
		t.Fatalf("first event = %s, want finish_reason:length", first)
	}

	// Second event: the [DONE] sentinel terminates the SSE stream.
	if string(events[1].Data) != "[DONE]" {
		t.Fatalf("second event = %s, want [DONE]", events[1].Data)
	}
}
