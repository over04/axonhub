package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

// Process executes the non-streaming LLM pipeline
// Steps: outbound transform -> HTTP request -> outbound response transform -> inbound response transform.
func (p *pipeline) notStream(
	ctx context.Context,
	executor Executor,
	request *httpclient.Request,
) (*httpclient.Response, error) {
	httpResp, err := executor.Do(ctx, request)
	if err != nil {
		// Apply error response middlewares
		p.applyRawErrorResponseMiddlewares(ctx, err)

		if httpErr, ok := errors.AsType[*httpclient.Error](err); ok {
			return nil, WrapUpstreamError(p.Outbound.TransformError(ctx, httpErr))
		}

		return nil, WrapUpstreamError(fmt.Errorf("failed to do request: %w", err))
	}

	// Apply raw response middlewares
	httpResp, err = p.applyRawResponseMiddlewares(ctx, httpResp)
	if err != nil {
		p.applyRawErrorResponseMiddlewares(ctx, err)

		return nil, fmt.Errorf("failed to apply raw response middlewares: %w", err)
	}

	llmResp, err := p.Outbound.TransformResponse(ctx, httpResp)
	if err != nil {
		p.applyRawErrorResponseMiddlewares(ctx, err)

		return nil, WrapUpstreamError(fmt.Errorf("failed to transform response: %w", err))
	}

	// Apply LLM response middlewares
	llmResp, err = p.applyLlmResponseMiddlewares(ctx, llmResp)
	if err != nil {
		p.applyRawErrorResponseMiddlewares(ctx, err)

		return nil, fmt.Errorf("failed to apply llm response middlewares: %w", err)
	}

	if p.emptyResponseDetection && !hasResponseContent(llmResp) {
		p.applyRawErrorResponseMiddlewares(ctx, ErrEmptyResponse)

		return nil, ErrEmptyResponse
	}

	slog.DebugContext(ctx, "LLM response", slog.Any("response", llmResp))

	finalResp, err := p.Inbound.TransformResponse(ctx, llmResp)
	if err != nil {
		p.applyRawErrorResponseMiddlewares(ctx, err)

		return nil, fmt.Errorf("failed to transform final response: %w", err)
	}

	// Apply inbound raw response middlewares after final response transformation
	finalResp, err = p.applyInboundRawResponseMiddlewares(ctx, finalResp)
	if err != nil {
		p.applyRawErrorResponseMiddlewares(ctx, err)

		return nil, fmt.Errorf("failed to apply inbound raw response middlewares: %w", err)
	}

	return finalResp, nil
}

func (p *pipeline) autoAggregateStream(
	ctx context.Context,
	executor Executor,
	request *httpclient.Request,
) (*httpclient.Response, error) {
	inboundStream, err := p.stream(ctx, executor, request, 0)
	if err != nil {
		return nil, err
	}
	defer inboundStream.Close()

	chunks := make([]*httpclient.StreamEvent, 0, 8)
	for inboundStream.Next() {
		event := inboundStream.Current()
		if event != nil {
			chunks = append(chunks, event)
		}
	}

	if err := inboundStream.Err(); err != nil {
		p.applyRawErrorResponseMiddlewares(ctx, err)
		return nil, err
	}

	if len(chunks) == 0 {
		p.applyRawErrorResponseMiddlewares(ctx, ErrEmptyStreamChunks)
		return nil, ErrEmptyStreamChunks
	}

	body, _, err := p.Inbound.AggregateStreamChunks(ctx, chunks)
	if err != nil {
		p.applyRawErrorResponseMiddlewares(ctx, err)
		return nil, err
	}

	if len(body) == 0 {
		p.applyRawErrorResponseMiddlewares(ctx, ErrEmptyAggregatedBody)
		return nil, ErrEmptyAggregatedBody
	}

	resp := &httpclient.Response{
		StatusCode: http.StatusOK,
		Headers: http.Header{
			"Content-Type":  []string{"application/json"},
			"Cache-Control": []string{"no-cache"},
		},
		Body: body,
	}

	resp, err = p.applyInboundRawResponseMiddlewares(ctx, resp)
	if err != nil {
		p.applyRawErrorResponseMiddlewares(ctx, err)
		return nil, fmt.Errorf("failed to apply inbound raw response middlewares: %w", err)
	}

	return resp, nil
}

// bufferAndReplayStream implements the "fakeStream" interruption policy.
// It initiates an upstream stream, buffers the entire response server-side,
// verifies the stream ended with the protocol's standard termination signal,
// and only then returns a stream that feeds the buffered chunks to the client
// as SSE. If the upstream stream ends abnormally (EOF/transport error without
// a termination signal), it returns ErrStreamInterrupted — which surfaces
// before any byte is sent to the client, so the existing retry flow in
// pipeline.Process can switch channels transparently.
//
// The buffering runs under the non-stream response timeout (the same overall
// deadline autoAggregateStream uses) so a slow-drip or hung upstream cannot pin
// the request. The first-event timeout is NOT applied during buffering: that
// timeout is about "flush the first event to the client" semantics, which is
// irrelevant here since nothing is flushed until buffering is complete. The
// overall duration is bounded by the request-level context instead.
func (p *pipeline) bufferAndReplayStream(
	ctx context.Context,
	executor Executor,
	request *httpclient.Request,
) (*Result, error) {
	timeoutCtx, cancel := p.withNonStreamTimeout(ctx)
	defer cancel()

	inboundStream, err := p.stream(timeoutCtx, executor, request, 0)
	if err != nil {
		if p.isNonStreamTimeout(timeoutCtx) {
			p.applyRawErrorResponseMiddlewares(ctx, ErrNonStreamResponseTimeout)
			return nil, ErrNonStreamResponseTimeout
		}
		return nil, err
	}
	defer inboundStream.Close()

	chunks := make([]*httpclient.StreamEvent, 0, 16)
	for inboundStream.Next() {
		if event := inboundStream.Current(); event != nil {
			chunks = append(chunks, event)
		}
	}

	if err := inboundStream.Err(); err != nil {
		if p.isNonStreamTimeout(timeoutCtx) {
			p.applyRawErrorResponseMiddlewares(ctx, ErrNonStreamResponseTimeout)
			return nil, ErrNonStreamResponseTimeout
		}
		// A mid-stream transport error is treated as an interruption: the
		// stream did not end with a standard termination signal, and no byte
		// has been sent to the client yet, so the retry flow can switch
		// channels transparently.
		p.applyRawErrorResponseMiddlewares(ctx, ErrStreamInterrupted)
		return nil, ErrStreamInterrupted
	}

	if len(chunks) == 0 {
		p.applyRawErrorResponseMiddlewares(ctx, ErrEmptyStreamChunks)
		return nil, ErrEmptyStreamChunks
	}

	if !streamEndedNormally(chunks) {
		p.applyRawErrorResponseMiddlewares(ctx, ErrStreamInterrupted)
		return nil, ErrStreamInterrupted
	}

	return &Result{
		Stream:      true,
		EventStream: streams.SliceStream(chunks),
	}, nil
}

// streamEndedNormally checks whether the buffered chunk sequence contains the
// protocol's standard termination signal, per each protocol's official spec:
//
//   - OpenAI: the "data: [DONE]" sentinel that terminates the SSE stream.
//     The preceding chunk carries choices[].finish_reason, but finish_reason
//     alone is NOT the terminator — a stream that emitted finish_reason and
//     then dropped before [DONE] did not end normally.
//     (https://platform.openai.com/docs/api-reference/chat/streaming)
//   - Anthropic: a message_stop event. The preceding message_delta carries
//     stop_reason+usage but is NOT terminal — a stream that emitted stop_reason
//     and then dropped before message_stop is interrupted.
//     (https://docs.claude.com/en/api/streaming)
//   - Gemini: a finishReason chunk. Gemini has no [DONE] sentinel; the
//     finishReason-bearing candidate is the terminal signal.
//     (googleapis google/ai/generativelanguage/v1beta)
//
// bufferAndReplayStream operates on already-inbound-transformed
// httpclient.StreamEvent (the final SSE bytes), so it inspects the raw payload.
func streamEndedNormally(chunks []*httpclient.StreamEvent) bool {
	for _, c := range chunks {
		if c == nil || len(c.Data) == 0 {
			continue
		}
		if isTerminationSentinel(c.Data) {
			return true
		}
	}
	return false
}

// isTerminationSentinel reports whether a raw SSE event is the protocol's
// terminal signal — the last event a well-formed stream emits:
//   - OpenAI: the bare [DONE] sentinel.
//   - Anthropic: a message_stop event (message_delta/stop_reason precedes it).
//   - Gemini: a finishReason-bearing chunk (camelCase; Gemini has no [DONE]).
//
// OpenAI finish_reason and Anthropic stop_reason are intentionally NOT matched
// here: they appear in the penultimate event, so matching them alone would
// misjudge a mid-stream drop (after stop_reason/finish_reason but before the
// terminal sentinel) as a normal end.
func isTerminationSentinel(data []byte) bool {
	// OpenAI: the [DONE] sentinel terminates the SSE stream.
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return true
	}
	// Anthropic: message_stop is the terminal event.
	if bytes.Contains(data, []byte(`"type":"message_stop"`)) {
		return true
	}
	// Gemini: finishReason (camelCase) is the terminal signal; there is no
	// [DONE]. Scoped to camelCase so OpenAI's snake_case finish_reason (which
	// precedes [DONE] and is not terminal) is not matched.
	return hasFinishReasonInRaw(data, []byte("finishReason"))
}

// hasFinishReasonInRaw reports whether the raw SSE data bytes contain a
// finish-reason field (from the given keys) with a non-null, non-empty-string
// value, indicating a termination signal. It matches quoted JSON field names
// only, requiring the opening quote to be preceded by a structural char (start
// of object or after a comma) so a literal occurrence inside a string value
// (e.g. delta.content mentioning "finish_reason") does not match.
func hasFinishReasonInRaw(data []byte, keys ...[]byte) bool {
	for _, key := range keys {
		if hasNonNullOrAfter(data, key) {
			return true
		}
	}
	return false
}

// finishReasonKeys are the finish-reason field names emitted by the supported
// inbound protocols: OpenAI/Anthropic use snake_case, Gemini uses camelCase.
var finishReasonKeys = [][]byte{
	[]byte("finish_reason"),
	[]byte("stop_reason"),
	[]byte("finishReason"),
}

// allFinishReasonKeys aliases finishReasonKeys for callers that want every
// supported field (the historical behavior used by completingStream).
func allFinishReasonKeys() [][]byte { return finishReasonKeys }

// hasNonNullOrAfter reports whether the JSON field named by key appears in data
// as a structural key (the quoted "key" preceded by '{' or ',') followed by a
// non-null, non-empty value. It skips the optional whitespace/colon between the
// key and the value, then checks the value does not start with 'n' (JSON null)
// and is not an empty string (""). An empty-string value (e.g.
// finish_reason:"") is not a valid termination signal — OpenAI uses null for
// "not finished", and an empty string is an abnormal payload that must not be
// mistaken for a real stop reason. A non-empty quoted value (e.g. "STOP") is
// valid and distinguished from "" by checking the second character.
func hasNonNullOrAfter(data []byte, key []byte) bool {
	quoted := append([]byte{'"'}, append(key, '"')...)
	keyLen := len(quoted)
	from := 0
	for {
		idx := bytes.Index(data[from:], quoted)
		if idx < 0 {
			return false
		}
		abs := from + idx
		// Require a structural boundary immediately before the quoted key so a
		// literal occurrence inside a string value does not match.
		if abs == 0 || data[abs-1] == '{' || data[abs-1] == ',' {
			rest := data[abs+keyLen:]
			for len(rest) > 0 {
				ch := rest[0]
				if ch == ' ' || ch == ':' || ch == '\t' || ch == '\n' || ch == '\r' {
					rest = rest[1:]
					continue
				}
				break
			}
			if len(rest) > 0 && rest[0] != 'n' && !bytes.HasPrefix(rest, []byte(`""`)) {
				return true
			}
		}
		from = abs + keyLen
	}
}
