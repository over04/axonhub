package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

type fakeCompleteInner struct {
	events []*httpclient.StreamEvent
	idx    int
}

func (f *fakeCompleteInner) Next() bool {
	if f.idx >= len(f.events) {
		return false
	}
	f.idx++
	return true
}
func (f *fakeCompleteInner) Current() *httpclient.StreamEvent { return f.events[f.idx-1] }
func (f *fakeCompleteInner) Err() error                      { return nil }
func (f *fakeCompleteInner) Close() error                    { return nil }

type fakeCompleter struct {
	extra []*httpclient.StreamEvent
}

func (f *fakeCompleter) CompletionEvents(_ context.Context) []*httpclient.StreamEvent {
	return f.extra
}

func TestCompletingStream_AppendsCompletionEventsWhenNoTermination(t *testing.T) {
	inner := &fakeCompleteInner{events: []*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{"content":"hi"}}]}`)},
		{Data: []byte(`{"choices":[{"delta":{"content":"!"}}]}`)},
	}}
	completer := &fakeCompleter{extra: []*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{},"finish_reason":"length"}]}`)},
		{Data: []byte("[DONE]")},
	}}
	s := newCompletingStream(inner, completer, context.Background())

	var got [][]byte
	for s.Next() {
		got = append(got, s.Current().Data)
	}
	if s.Err() != nil {
		t.Fatalf("Err() should be nil, got %v", s.Err())
	}
	if len(got) != 4 {
		t.Fatalf("got %d events, want 4", len(got))
	}
	if string(got[3]) != "[DONE]" {
		t.Fatalf("last event = %s, want [DONE]", got[3])
	}
}

func TestCompletingStream_NoAppendWhenTerminationPresent(t *testing.T) {
	inner := &fakeCompleteInner{events: []*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)},
		{Data: []byte("[DONE]")},
	}}
	completer := &fakeCompleter{extra: []*httpclient.StreamEvent{
		{Data: []byte("SHOULD_NOT_APPEAR")},
	}}
	s := newCompletingStream(inner, completer, context.Background())

	var got [][]byte
	for s.Next() {
		got = append(got, s.Current().Data)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (no append)", len(got))
	}
	for _, e := range got {
		if string(e) == "SHOULD_NOT_APPEAR" {
			t.Fatalf("completion event should not have been appended")
		}
	}
}

// TestCompletingStream_NoAppendWhenBareDoneSentinel: a bare [DONE] without a
// prior finish_reason chunk still counts as termination (mirrors
// streamEndedNormally), so no synthetic events are appended after [DONE].
func TestCompletingStream_NoAppendWhenBareDoneSentinel(t *testing.T) {
	inner := &fakeCompleteInner{events: []*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{"content":"hi"}}]}`)},
		{Data: []byte("[DONE]")},
	}}
	completer := &fakeCompleter{extra: []*httpclient.StreamEvent{
		{Data: []byte("SHOULD_NOT_APPEAR")},
	}}
	s := newCompletingStream(inner, completer, context.Background())

	var got [][]byte
	for s.Next() {
		got = append(got, s.Current().Data)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (no append after bare [DONE])", len(got))
	}
	for _, e := range got {
		if string(e) == "SHOULD_NOT_APPEAR" {
			t.Fatalf("completion event should not have been appended after [DONE]")
		}
	}
}

type errInner struct{}

func (e *errInner) Next() bool                       { return false }
func (e *errInner) Current() *httpclient.StreamEvent { return nil }
func (e *errInner) Err() error                       { return errInnerErr }
func (e *errInner) Close() error                     { return nil }

var errInnerErr = errors.New("inner error")

func TestCompletingStream_NoAppendWhenInnerErr(t *testing.T) {
	inner := &errInner{}
	completer := &fakeCompleter{extra: []*httpclient.StreamEvent{{Data: []byte("X")}}}
	s := newCompletingStream(inner, completer, context.Background())
	for s.Next() {
		t.Fatalf("should not yield when inner errored")
	}
}

var _ streams.Stream[*httpclient.StreamEvent] = (*fakeCompleteInner)(nil)
