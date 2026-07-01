package gemini

import (
	"context"
	"strings"
	"testing"
)

// TestInboundTransformer_CompletionEvents verifies the synthesized termination
// event matches the Gemini streamGenerateContent protocol:
//
//	data: {"candidates":[{"index":0,"finishReason":"MAX_TOKENS",...}]}
//
// Per the Gemini API (googleapis proto): the stream is a server-streaming RPC
// with NO [DONE] sentinel; termination is signaled by finishReason in the final
// candidate. MAX_TOKENS (=2) means "the maximum number of tokens as specified
// in the request was reached" — the honest mapping for a mid-stream drop.
// The enum uses uppercase (STOP/MAX_TOKENS/SAFETY/RECITATION/OTHER/...); there
// is no "length" value.
func TestInboundTransformer_CompletionEvents(t *testing.T) {
	tt := &InboundTransformer{}
	events := tt.CompletionEvents(context.Background())
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	data := string(events[0].Data)
	if !strings.Contains(data, `"finishReason":"MAX_TOKENS"`) {
		t.Fatalf("event = %s, want finishReason:MAX_TOKENS", data)
	}
	// Gemini signals termination via finishReason inside candidates[]; there is
	// no OpenAI-style [DONE] sentinel in the Gemini protocol.
	if strings.Contains(data, "[DONE]") {
		t.Fatalf("event = %s, Gemini must NOT emit a [DONE] sentinel", data)
	}
}
