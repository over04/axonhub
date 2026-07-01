package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
)

type firstEventTimeoutGuard struct {
	timer    *time.Timer
	cancel   context.CancelFunc
	stopOnce sync.Once
	state    atomic.Uint32
}

const (
	firstEventPending uint32 = iota
	firstEventCompleted
	firstEventTimedOut
)

func newFirstEventTimeoutGuard(ctx context.Context, timeout time.Duration) (context.Context, *firstEventTimeoutGuard) {
	if timeout <= 0 {
		return ctx, nil
	}

	streamCtx, cancel := context.WithCancel(ctx)
	guard := &firstEventTimeoutGuard{
		cancel: cancel,
	}
	guard.timer = time.AfterFunc(timeout, func() {
		if guard.state.CompareAndSwap(firstEventPending, firstEventTimedOut) {
			cancel()
		}
	})

	return streamCtx, guard
}

func (g *firstEventTimeoutGuard) timedOut() bool {
	return g != nil && g.state.Load() == firstEventTimedOut
}

func (g *firstEventTimeoutGuard) stop() {
	if g == nil {
		return
	}

	g.stopOnce.Do(func() {
		g.timer.Stop()
	})
}

func (g *firstEventTimeoutGuard) cancelStream() {
	if g == nil {
		return
	}

	g.cancel()
}

func (g *firstEventTimeoutGuard) completeFirstEventPhase() {
	if g == nil {
		return
	}

	g.state.CompareAndSwap(firstEventPending, firstEventCompleted)
	g.stop()
}

func (g *firstEventTimeoutGuard) acceptFirstEvent() bool {
	if g == nil {
		return true
	}

	// If the timeout already canceled the stream, keep that state so callers
	// can retry instead of returning a partially usable stream.
	if g.state.CompareAndSwap(firstEventPending, firstEventCompleted) {
		g.stop()

		return true
	}

	return g.state.Load() == firstEventCompleted
}

func (g *firstEventTimeoutGuard) finishBeforeFirstEvent(err error) error {
	if g == nil {
		return err
	}

	g.completeFirstEventPhase()
	g.cancelStream()
	if g.timedOut() {
		return ErrStreamFirstEventTimeout
	}

	return err
}

type cancelOnCloseStream struct {
	stream streams.Stream[*httpclient.StreamEvent]
	cancel context.CancelFunc
	once   sync.Once
}

func (s *cancelOnCloseStream) Next() bool {
	return s.stream.Next()
}

func (s *cancelOnCloseStream) Current() *httpclient.StreamEvent {
	return s.stream.Current()
}

func (s *cancelOnCloseStream) Err() error {
	return s.stream.Err()
}

func (s *cancelOnCloseStream) Close() error {
	err := s.stream.Close()
	s.once.Do(s.cancel)

	return err
}

// hasFinishReason checks if an llm.Response event contains a finish reason.
func hasFinishReason(resp *llm.Response) bool {
	if resp == nil {
		return false
	}

	for _, choice := range resp.Choices {
		if choice.FinishReason != nil {
			return true
		}
	}

	return false
}

// preReadLlmStream reads the initial LLM stream events so the pipeline can
// enforce first-event timeout and, when enabled, detect empty responses.
// If the stream contains content, it returns a new stream with the pre-read events prepended.
// If the stream is empty (finish reason reached without content), it returns ErrEmptyResponse.
func (p *pipeline) preReadLlmStream(
	ctx context.Context,
	llmStream streams.Stream[*llm.Response],
	firstEventGuard *firstEventTimeoutGuard,
) (streams.Stream[*llm.Response], error) {
	const maxPreReadEvents = 3
	preReadLimit := maxPreReadEvents
	if !p.emptyResponseDetection {
		preReadLimit = 1
	}

	var buffered []*llm.Response

	for i := range preReadLimit {
		hasNext, err := nextLlmStreamEvent(ctx, llmStream, i == 0, firstEventGuard)
		if err != nil {
			llmStream.Close()

			return nil, err
		}
		if !hasNext {
			break
		}

		event := llmStream.Current()
		buffered = append(buffered, event)

		if !p.emptyResponseDetection {
			return streams.PrependStream(llmStream, buffered...), nil
		}

		if hasResponseContent(event) {
			// Has content, not empty — prepend buffered events back
			return streams.PrependStream(llmStream, buffered...), nil
		}

		// Recognize both the shared sentinel pointer (llm.DoneResponse) and a
		// freshly-constructed "[DONE]" terminator: outbound transformers that emit
		// terminal events as new *llm.Response (e.g. OpenAI TTS binary streams) must
		// still trigger empty-response handling when no audio chunks were produced.
		if event == llm.DoneResponse || (event != nil && event.Object == "[DONE]") || hasFinishReason(event) {
			// Reached end without content — empty response
			slog.WarnContext(ctx, "empty response detected",
				slog.Int("events_read", len(buffered)),
			)

			llmStream.Close()

			return nil, ErrEmptyResponse
		}
	}

	if err := llmStream.Err(); err != nil {
		llmStream.Close()

		return nil, err
	}

	// Didn't find content or finish in 3 events — treat as non-empty (safe default)
	if len(buffered) > 0 {
		return streams.PrependStream(llmStream, buffered...), nil
	}

	return llmStream, nil
}

func nextLlmStreamEvent(
	ctx context.Context,
	llmStream streams.Stream[*llm.Response],
	firstEvent bool,
	firstEventGuard *firstEventTimeoutGuard,
) (bool, error) {
	if !firstEvent || firstEventGuard == nil {
		return llmStream.Next(), nil
	}

	hasNext := llmStream.Next()
	if hasNext {
		if !firstEventGuard.acceptFirstEvent() {
			llmStream.Close()

			return false, ErrStreamFirstEventTimeout
		}

		return true, nil
	}

	firstEventGuard.completeFirstEventPhase()
	if firstEventGuard.timedOut() {
		llmStream.Close()

		return false, ErrStreamFirstEventTimeout
	}

	if ctx.Err() != nil {
		firstEventGuard.cancelStream()
		llmStream.Close()

		return false, ctx.Err()
	}

	return hasNext, nil
}

// Process executes the streaming LLM pipeline
// Steps: outbound transform -> HTTP stream -> outbound stream transform -> inbound stream transform.
func (p *pipeline) stream(
	ctx context.Context,
	executor Executor,
	request *httpclient.Request,
	firstEventTimeout time.Duration,
) (streams.Stream[*httpclient.StreamEvent], error) {
	streamCtx, firstEventGuard := newFirstEventTimeoutGuard(ctx, firstEventTimeout)

	outboundStream, err := executor.DoStream(streamCtx, request)
	if firstEventGuard.timedOut() {
		if outboundStream != nil {
			outboundStream.Close()
		}

		timeoutErr := firstEventGuard.finishBeforeFirstEvent(ErrStreamFirstEventTimeout)
		p.applyRawErrorResponseMiddlewares(ctx, timeoutErr)

		return nil, timeoutErr
	}
	if err != nil {
		err = firstEventGuard.finishBeforeFirstEvent(err)

		// Apply error response middlewares
		p.applyRawErrorResponseMiddlewares(ctx, err)

		if errors.Is(err, ErrStreamFirstEventTimeout) {
			return nil, err
		}

		if httpErr, ok := errors.AsType[*httpclient.Error](err); ok {
			return nil, WrapUpstreamError(p.Outbound.TransformError(ctx, httpErr))
		}

		return nil, WrapUpstreamError(err)
	}

	// Apply raw stream middlewares
	rawStream := outboundStream

	outboundStream, err = p.applyRawStreamMiddlewares(ctx, outboundStream)
	if err != nil {
		rawStream.Close()
		err = firstEventGuard.finishBeforeFirstEvent(err)
		p.applyRawErrorResponseMiddlewares(ctx, err)

		if errors.Is(err, ErrStreamFirstEventTimeout) {
			return nil, err
		}

		return nil, fmt.Errorf("failed to apply raw stream middlewares: %w", err)
	}

	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		outboundStream = streams.Map(outboundStream,
			func(event *httpclient.StreamEvent) *httpclient.StreamEvent {
				slog.DebugContext(ctx, "Outbound stream event", slog.Any("event", event))
				return event
			},
		)
	}

	llmStream, err := p.Outbound.TransformStream(ctx, request, outboundStream)
	if err != nil {
		outboundStream.Close()
		err = firstEventGuard.finishBeforeFirstEvent(err)
		p.applyRawErrorResponseMiddlewares(ctx, err)

		slog.ErrorContext(ctx, "Failed to transform streaming request", slog.Any("error", err))

		if errors.Is(err, ErrStreamFirstEventTimeout) {
			return nil, err
		}

		return nil, WrapUpstreamError(err)
	}

	rawLlmStream := llmStream

	// Apply LLM stream middlewares
	llmStream, err = p.applyLlmStreamMiddlewares(ctx, llmStream)
	if err != nil {
		rawLlmStream.Close()
		err = firstEventGuard.finishBeforeFirstEvent(err)
		p.applyRawErrorResponseMiddlewares(ctx, err)

		if errors.Is(err, ErrStreamFirstEventTimeout) {
			return nil, err
		}

		return nil, fmt.Errorf("failed to apply llm stream middlewares: %w", err)
	}

	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		llmStream = streams.Map(llmStream, func(event *llm.Response) *llm.Response {
			slog.DebugContext(ctx, "LLM stream event", slog.Any("event", event))
			return event
		})
	}

	// Check stream start for first-event timeout or empty response detection.
	if p.emptyResponseDetection || firstEventTimeout > 0 {
		rawLlmStream := llmStream

		llmStream, err = p.preReadLlmStream(ctx, llmStream, firstEventGuard)
		if err != nil {
			rawLlmStream.Close()
			err = firstEventGuard.finishBeforeFirstEvent(err)
			p.applyRawErrorResponseMiddlewares(ctx, err)

			return nil, err
		}
	} else if firstEventGuard != nil {
		firstEventGuard.stop()
	}

	inboundStream, err := p.Inbound.TransformStream(ctx, llmStream)
	if err != nil {
		llmStream.Close()
		firstEventGuard.cancelStream()
		p.applyRawErrorResponseMiddlewares(ctx, err)

		slog.ErrorContext(ctx, "Failed to transform streaming request", slog.Any("error", err))

		return nil, err
	}

	rawInboundStream := inboundStream

	inboundStream, err = p.applyInboundRawStreamMiddlewares(ctx, inboundStream)
	if err != nil {
		rawInboundStream.Close()
		firstEventGuard.cancelStream()
		p.applyRawErrorResponseMiddlewares(ctx, err)

		return nil, fmt.Errorf("failed to apply inbound raw stream middlewares: %w", err)
	}

	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		inboundStream = streams.Map(
			inboundStream,
			func(event *httpclient.StreamEvent) *httpclient.StreamEvent {
				slog.DebugContext(ctx, "Inbound stream event", slog.Any("event", event))
				return event
			},
		)
	}

	if firstEventGuard != nil {
		inboundStream = &cancelOnCloseStream{
			stream: inboundStream,
			cancel: firstEventGuard.cancelStream,
		}
	}

	return inboundStream, nil
}

// completingStream wraps an inbound SSE stream and, when the upstream stream
// ends without the protocol's standard termination signal, appends the
// termination events produced by the inbound transformer's StreamCompleter.
// This is the "complete" stream-interruption policy: the client ends cleanly
// (no error, no retry) but sees a length-capped (max_tokens/length) stop reason
// rather than the true mid-stream-drop cause.
type completingStream struct {
	inner     streams.Stream[*httpclient.StreamEvent]
	completer transformer.StreamCompleter
	ctx       context.Context

	sawTermination bool
	phase          int // 0 = draining inner, 1 = yielding appended events
	appended       []*httpclient.StreamEvent
	appendedIdx    int
}

func newCompletingStream(
	inner streams.Stream[*httpclient.StreamEvent],
	completer transformer.StreamCompleter,
	ctx context.Context,
) *completingStream {
	if ctx == nil {
		ctx = context.Background()
	}
	return &completingStream{
		inner:     inner,
		completer: completer,
		ctx:       ctx,
	}
}

// isTerminationChunk reports whether a raw SSE event carries the protocol's
// terminal signal — the last meaningful event a well-formed stream emits.
// It mirrors isTerminationSentinel (non_streaming.go): OpenAI [DONE] or
// finish_reason, Anthropic message_stop, Gemini finishReason. Anthropic
// stop_reason is intentionally NOT matched: it lives in message_delta, which
// precedes message_stop, so matching it alone would suppress the synthesized
// message_stop after a mid-stream drop.
func isTerminationChunk(data []byte) bool {
	return isTerminationSentinel(data)
}

func (s *completingStream) Next() bool {
	if s.phase == 0 {
		if s.inner.Next() {
			if cur := s.inner.Current(); cur != nil && isTerminationChunk(cur.Data) {
				s.sawTermination = true
			}
			return true
		}
		// inner ended; transition to phase 1 if we should append
		s.phase = 1
		if s.inner.Err() != nil {
			return false
		}
		if s.sawTermination {
			return false
		}
		if s.completer == nil {
			return false
		}
		s.appended = s.completer.CompletionEvents(s.ctx)
		return s.appendedIdx < len(s.appended)
	}
	// phase 1
	s.appendedIdx++
	return s.appendedIdx < len(s.appended)
}

func (s *completingStream) Current() *httpclient.StreamEvent {
	if s.phase == 0 {
		return s.inner.Current()
	}
	if s.appendedIdx < 0 || s.appendedIdx >= len(s.appended) {
		return nil
	}
	return s.appended[s.appendedIdx]
}

func (s *completingStream) Err() error {
	return s.inner.Err()
}

func (s *completingStream) Close() error {
	return s.inner.Close()
}

var _ streams.Stream[*httpclient.StreamEvent] = (*completingStream)(nil)
