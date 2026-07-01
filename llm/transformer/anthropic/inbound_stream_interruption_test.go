package anthropic

import (
	"context"
	"strings"
	"testing"
)

// TestInboundTransformer_CompletionEvents verifies the synthesized termination
// events match the Anthropic streaming protocol:
//
//	event: message_delta
//	data: {"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null},"usage":{"output_tokens":...}}
//	event: message_stop
//	data: {"type":"message_stop"}
//
// Per https://docs.claude.com/en/api/streaming: message_delta carries the
// stop_reason and a cumulative usage object; message_stop is the terminal
// event. Both must carry their type so the SSE writer emits the event: line
// (Anthropic clients dispatch on the event name).
func TestInboundTransformer_CompletionEvents(t *testing.T) {
	tt := &InboundTransformer{}
	events := tt.CompletionEvents(context.Background())
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	// First event: message_delta with stop_reason=max_tokens and a usage object.
	first := string(events[0].Data)
	if !strings.Contains(first, `"type":"message_delta"`) {
		t.Fatalf("first event = %s, want type message_delta", first)
	}
	if !strings.Contains(first, `"stop_reason":"max_tokens"`) {
		t.Fatalf("first event = %s, want stop_reason:max_tokens", first)
	}
	// Official message_delta includes a usage object (cumulative output_tokens).
	// The synthesized event provides a zero-value usage so consumers see a
	// defined shape rather than nil.
	if !strings.Contains(first, `"usage"`) {
		t.Fatalf("first event = %s, want a usage object", first)
	}

	// Second event: message_stop — the terminal sentinel.
	second := string(events[1].Data)
	if !strings.Contains(second, `"type":"message_stop"`) {
		t.Fatalf("second event = %s, want type message_stop", second)
	}
}
