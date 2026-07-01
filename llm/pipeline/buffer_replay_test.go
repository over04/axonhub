package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

// fakeStreamResolver implements StreamInterruptionResolver for tests, returning
// a fixed policy. Embed *mockOutbound to inherit the other Outbound methods.
type fakeStreamResolver struct {
	*mockOutbound
	policy StreamInterruptionPolicy
}

func (f *fakeStreamResolver) ResolveStreamInterruption(_ context.Context) StreamInterruptionPolicy {
	return f.policy
}

// TestBufferAndReplayStream_CompleteReplaysAllChunks: 上游流出完整终止信号 →
// 返回 Stream=true，EventStream 按 yield 全部 chunk，且最后一个是终止事件。
func TestBufferAndReplayStream_CompleteReplaysAllChunks(t *testing.T) {
	ctx := context.Background()
	executor := &mockExecutor{
		doStream: func(ctx context.Context, req *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
			return streams.SliceStream([]*httpclient.StreamEvent{{}}), nil
		},
	}
	outbound := &fakeStreamResolver{
		mockOutbound: &mockOutbound{
			transformStream: func(ctx context.Context, req *httpclient.Request, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
				return streams.SliceStream([]*llm.Response{
					{Choices: []llm.Choice{{Delta: &llm.Message{Content: llm.MessageContent{Content: lo.ToPtr("hi")}}}}},
					llm.DoneResponse,
				}), nil
			},
		},
		policy: StreamInterruptionFakeStream,
	}
	streamFlag := true
	inbound := &mockInbound{
		transformStream: func(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*httpclient.StreamEvent], error) {
			return streams.MapErr(stream, func(resp *llm.Response) (*httpclient.StreamEvent, error) {
				if resp == llm.DoneResponse {
					return &httpclient.StreamEvent{Data: []byte("[DONE]")}, nil
				}
				return &httpclient.StreamEvent{Data: []byte(`{"choices":[{"delta":{"content":"hi"}}]}`)}, nil
			}), nil
		},
	}
	p := &pipeline{
		Executor: executor,
		Inbound:  inbound,
		Outbound: outbound,
	}

	res, err := p.processRequest(ctx, &llm.Request{Stream: &streamFlag})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.Stream)
	require.NotNil(t, res.EventStream)

	var got [][]byte
	for res.EventStream.Next() {
		got = append(got, res.EventStream.Current().Data)
	}
	require.NoError(t, res.EventStream.Err())
	require.Len(t, got, 2)
	require.Equal(t, "[DONE]", string(got[1]))
}

// TestBufferAndReplayStream_InterruptedReturnsErr: 上游流 EOF 但无终止信号 →
// 返回 ErrStreamInterrupted，EventStream 为 nil(客户端零字节未发)。
func TestBufferAndReplayStream_InterruptedReturnsErr(t *testing.T) {
	ctx := context.Background()
	executor := &mockExecutor{
		doStream: func(ctx context.Context, req *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
			return streams.SliceStream([]*httpclient.StreamEvent{{}}), nil
		},
	}
	outbound := &fakeStreamResolver{
		mockOutbound: &mockOutbound{
			transformStream: func(ctx context.Context, req *httpclient.Request, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
				// 仅一个 delta chunk，无 finish_reason、无 [DONE] —— 模拟上游中途断开
				return streams.SliceStream([]*llm.Response{
					{Choices: []llm.Choice{{Delta: &llm.Message{Content: llm.MessageContent{Content: lo.ToPtr("hi")}}}}},
				}), nil
			},
		},
		policy: StreamInterruptionFakeStream,
	}
	streamFlag := true
	inbound := &mockInbound{
		transformStream: func(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*httpclient.StreamEvent], error) {
			return streams.MapErr(stream, func(resp *llm.Response) (*httpclient.StreamEvent, error) {
				return &httpclient.StreamEvent{Data: []byte(`{"choices":[{"delta":{"content":"hi"}}]}`)}, nil
			}), nil
		},
	}
	p := &pipeline{
		Executor: executor,
		Inbound:  inbound,
		Outbound: outbound,
	}

	_, err := p.processRequest(ctx, &llm.Request{Stream: &streamFlag})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrStreamInterrupted), "expected ErrStreamInterrupted, got %v", err)
}

// TestStreamEndedNormally verifies the per-protocol terminal signal detection
// against the official SSE specs:
//   - OpenAI: stream ends with "data: [DONE]". A finish_reason chunk alone
//     (without [DONE]) is a mid-stream drop, NOT normally ended.
//   - Anthropic: stream ends with a message_stop event. A message_delta
//     (stop_reason) alone is a mid-stream drop.
//   - Gemini: stream ends with a finishReason-bearing chunk (no [DONE]).
func TestStreamEndedNormally(t *testing.T) {
	// OpenAI: [DONE] is the terminal sentinel.
	require.True(t, streamEndedNormally([]*httpclient.StreamEvent{{Data: []byte("[DONE]")}}))
	// OpenAI: finish_reason chunk followed by [DONE] is normal.
	require.True(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)},
		{Data: []byte("[DONE]")},
	}))
	// OpenAI: a finish_reason chunk WITHOUT a trailing [DONE] is a mid-stream
	// drop — [DONE] is the terminator, not finish_reason.
	require.False(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)},
	}))
	// Anthropic: message_delta(stop_reason) + message_stop is normal.
	require.True(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)},
		{Data: []byte(`{"type":"message_stop"}`)},
	}))
	// Anthropic: message_delta WITHOUT message_stop is a mid-stream drop.
	require.False(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)},
	}))
	// Gemini uses camelCase finishReason and no [DONE] sentinel: the
	// finishReason chunk itself is the terminal signal.
	require.True(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"candidates":[{"content":{"role":"model"},"finishReason":"STOP","index":0}]}`)},
	}))
	// A delta-only chunk is not a terminal sentinel.
	require.False(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{"content":"hi"}}]}`)},
	}))
	// null finish_reason is not a terminal signal.
	require.False(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{},"finish_reason":null}]}`)},
	}))
	// An empty-string finish_reason is an abnormal payload and must NOT be
	// mistaken for a valid stop reason.
	require.False(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{},"finish_reason":""}]}`)},
	}))
	// A literal "finish_reason" inside a string value must NOT match.
	require.False(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{"content":"the finish_reason field"}}]}`)},
	}))
	require.False(t, streamEndedNormally(nil))
}
