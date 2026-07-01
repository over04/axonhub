# 渠道级 SSE 流中断恢复策略 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在渠道设置中新增"流式中断恢复策略"(无 / 补齐标准结束 / 假流式，默认假流式)，解决上游 SSE 流首事件之后中途断开导致客户端收到无终止语义的截断流的问题。

**Architecture:** 三种模式。模式 3(假流式)复用 `autoAggregateStream` 的"向上游发流式 + 全量缓冲"逻辑，缓冲完整后把 chunks 切片回放为 SSE 流；缓冲阶段失败走现有重试循环(客户端零字节未发，完全透明)。模式 2 在 inbound 流末端补发该协议标准终止事件(`max_tokens`/`length` 语义)，覆盖 OpenAI、Anthropic、Gemini 全部协议。模式 1 不处理。策略字段加在 `ChannelSettings`(`*T` 指针风格，nil=继承全局默认)，全局默认值加在 `RetryPolicy`。

**Tech Stack:** Go(Gin/Ent/gqlgen/FX)、React 19 + TypeScript + TanStack Query。`llm/` 是独立 Go module，所有 `llm/` 相关 `go build`/`go test` 必须在 `llm/` 目录下执行。

**Spec:** `docs/superpowers/specs/2026-06-30-stream-interruption-recovery-design.md`

---

## 已核实的代码事实(实现依据)

执行者直接使用以下事实，无需再查：

- **pipeline 现有测试 fake 模式**(`llm/pipeline/empty_response_test.go`)：用 `mockExecutor`/`mockOutbound`/`mockInbound` + `streams.SliceStream` 构造流。`streams.SliceStream[T](items []T) Stream[T]` 在 `llm/streams/slice.go:4`。
- **流终止标记**：`llm.DoneResponse`(`llm/model.go:18`，`Object == "[DONE]"`)是统一结束标记；`hasFinishReason`(`llm/pipeline/stream.go:135`)判断 Choice 有 FinishReason。
- **inbound transformer 类型名**：三个协议都是 `InboundTransformer`(空 struct)。
  - OpenAI：`llm/transformer/openai/inbound.go:19`，有 `TransformStreamChunk(ctx, *llm.Response) (*httpclient.StreamEvent, error)`(`:112`)，对 `chatResp.Object == "[DONE]"` 产出 `Data: []byte("[DONE]")`(`:120-124`)，否则 `ResponseFromLLM(chatResp)` 后 `json.Marshal`。
  - Gemini：`llm/transformer/gemini/inbound_stream.go:329`，有 `TransformStreamChunk(ctx, *llm.Response) (*httpclient.StreamEvent, error)`，对 `[DONE]` 返回 nil(Gemini 不用 `[DONE]`)，否则 `convertLLMToGeminiResponse(out, true)` 后 `json.Marshal`(`:318-325`)。Gemini 终止靠 `candidates[].finishReason`。
  - Anthropic：**无** `TransformStreamChunk`(流式是 stateful 状态机 `inbound_stream.go`)。补齐事件直接构造 `StreamEvent` struct 后 `json.Marshal`。`StreamEvent`/`StreamDelta` 在 `llm/transformer/anthropic/model.go:449/470`。`enqueEvent`(`inbound_stream.go:292`)即 `json.Marshal(ev)` → `httpclient.StreamEvent{Data: eventData}`。
- **GraphQL**：`ChannelSettings` 是 `internal/server/gql/axonhub.graphql:87` 的显式 type，`ChannelSettingsInput` 在 `:123`。`RetryPolicy` type 在 `internal/server/gql/system.graphql:121`，`UpdateRetryPolicyInput` 在 `:154`。gqlgen resolver 在 `internal/server/gql/system.resolvers.go` 直接用 `biz.RetryPolicy`。
- **orchestrator 运行时状态**：pipeline option 在 `orchestrator.go:210` 装配时 channel candidate 尚未选定(`state.CurrentCandidate == nil`)，候选选择发生在 `pipe.Process` 内部的 `selectCandidates` middleware。所以策略**不能**静态注入，必须运行时解析。`PersistentOutboundTransformer`(`outbound.go:315`)持有 `state *PersistenceState`，`state.RetryPolicyProvider`(`state.go:22`)是 `RetryPolicyProvider` 接口(`load_balancer.go:82`，方法 `RetryPolicyOrDefault(ctx) *biz.RetryPolicy`)。`PersistentOutboundTransformer.GetCurrentChannel() *biz.Channel`(`outbound.go:482`)在运行时返回当前渠道，`channel.Settings` 是 `*objects.ChannelSettings`。`isPassThroughEnabled(ctx, *biz.SystemService)`(`pass_through.go:25`)是现有 pass-through 判定。
- **前端渠道 dialog 模板**：`frontend/src/features/channels/components/channels-rate-limit-dialog.tsx`，用 `useUpdateChannel` + `mergeChannelSettingsForUpdate(currentRow.settings, patch)`。前端 channel schema 在 `frontend/src/features/channels/data/schema.ts:219`。

---

## 文件结构总览

**后端 — `internal/`(主 module)**
- `internal/objects/channel.go` — 新增 `StreamInterruptionPolicy` 类型与 `ChannelSettings.StreamInterruption` 字段
- `internal/server/biz/system.go` — `RetryPolicy` 新增 `StreamInterruptionDefault` 字段 + `normalizeRetryPolicy` 规范化
- `internal/server/biz/system_default.go` — 全局默认值 `fakeStream`
- `internal/server/gql/system.graphql` + `axonhub.graphql` — GraphQL schema 暴露新字段 + 重生成 `generated.go`
- `internal/server/orchestrator/outbound.go` — `PersistentOutboundTransformer` 实现 `StreamInterruptionResolver`

**后端 — `llm/`(独立 module)**
- `llm/pipeline/pipeline.go` — `processRequest` 分支调整、`StreamInterruptionPolicy` 类型、`StreamInterruptionResolver` 接口、`streamForPolicy`、`wrapWithCompletingStream`
- `llm/pipeline/non_streaming.go` — 新增 `bufferAndReplayStream`、`streamEndedNormally`、`hasFinishReasonInRaw`、`hasNonNullOrAfter`
- `llm/pipeline/replay_stream.go` — 新增 `replayStream`(Create)
- `llm/pipeline/stream.go` — 新增 `completingStream`、`ErrStreamInterrupted`
- `llm/transformer/interfaces.go` — Inbound 新增 `StreamCompleter` 可选接口
- `llm/transformer/openai/inbound.go` — 实现 `CompletionEvents`
- `llm/transformer/anthropic/inbound.go` — 实现 `CompletionEvents`
- `llm/transformer/gemini/inbound.go` — 实现 `CompletionEvents`

**前端**
- `frontend/src/features/channels/data/schema.ts` — ChannelSettings schema 加字段
- `frontend/src/features/channels/components/channels-stream-interruption-dialog.tsx` — 新建设置 dialog(Create)
- `frontend/src/features/channels/components/channels-table.tsx` + actions — 接入入口
- `frontend/src/features/channels/components/index.ts` — 导出
- `frontend/src/features/system/data/system.ts` + `components/retry-settings.tsx` — 全局默认值 UI
- `frontend/src/locales/zh.json`、`en.json` — i18n

**代码生成**：GraphQL schema 改动后 `go generate ./internal/server/gql/...`；前端 GraphQL 改动后 `pnpm codegen`(以 `frontend/package.json` 实际脚本为准)。

---

## Task 1: 新增 `StreamInterruptionPolicy` 类型与渠道字段

**Files:**
- Modify: `internal/objects/channel.go`

- [ ] **Step 1: 在 `internal/objects/channel.go` 的 `RetryableErrorPattern` 结构体定义之前，新增类型与常量**

```go
// StreamInterruptionPolicy controls how the channel handles upstream SSE
// streams that end abnormally (connection drop mid-stream without the
// protocol's standard termination signal).
type StreamInterruptionPolicy string

const (
	// StreamInterruptionNone: no recovery; client receives the truncated stream (current behavior).
	StreamInterruptionNone StreamInterruptionPolicy = "none"
	// StreamInterruptionComplete: synthesize the protocol's standard termination
	// events (max_tokens / length semantics) so the client ends cleanly.
	// Note: this misrepresents a mid-stream drop as a length-capped truncation.
	StreamInterruptionComplete StreamInterruptionPolicy = "complete"
	// StreamInterruptionFakeStream: buffer the full upstream stream server-side,
	// replay to client as SSE only after the complete stream is received; a
	// mid-stream drop during buffering triggers the existing retry flow with
	// zero bytes sent to the client.
	StreamInterruptionFakeStream StreamInterruptionPolicy = "fakeStream"
)
```

- [ ] **Step 2: 在 `ChannelSettings` 结构体的 `RetryableErrorPatterns` 字段之后、结构体闭合 `}` 之前，新增字段**

```go
	// StreamInterruption controls the upstream stream-interruption recovery policy:
	// what to do when an upstream SSE stream ends abnormally (mid-stream drop
	// without the protocol's standard termination signal).
	// nil = inherit the global default (StreamInterruptionDefault on RetryPolicy);
	// otherwise overrides per channel.
	// Not effective when PassThroughBody is enabled — fake-stream buffering and
	// complete-event synthesis require the transform pipeline, so the policy is
	// forced to "none" in that case.
	StreamInterruption *StreamInterruptionPolicy `json:"streamInterruption,omitempty"`
```

- [ ] **Step 3: 验证主 module 编译**

Run: `go build ./internal/objects/...`
Expected: 编译通过，无错误。
---

## Task 2: 全局默认值 — `RetryPolicy` 新增字段

**Files:**
- Modify: `internal/server/biz/system.go`
- Modify: `internal/server/biz/system_default.go`

- [ ] **Step 1: 在 `RetryPolicy` 结构体(`internal/server/biz/system.go:351`，`UpstreamErrorPolicy UpstreamErrorPolicy` 字段之后)新增字段**

```go
	// StreamInterruptionDefault is the global default for a channel's
	// StreamInterruption policy when the channel leaves it nil (inherit).
	// Default: "fakeStream".
	StreamInterruptionDefault objects.StreamInterruptionPolicy `json:"stream_interruption_default"`
```

确认 `internal/server/biz` 已 import `github.com/looplj/axonhub/internal/objects`(若未引用则在 import 块补上)。

- [ ] **Step 2: 在 `internal/server/biz/system_default.go` 的 `defaultRetryPolicy` 变量末尾加默认值**

```go
	StreamInterruptionDefault: objects.StreamInterruptionFakeStream,
```

- [ ] **Step 3: 在 `normalizeRetryPolicy` 函数末尾加规范化**

Run: `grep -n "func normalizeRetryPolicy" internal/server/biz/system.go` 定位函数。在其函数体末尾(`}` 之前)加：

```go
	if policy.StreamInterruptionDefault == "" {
		policy.StreamInterruptionDefault = objects.StreamInterruptionFakeStream
	}
```

- [ ] **Step 4: 验证编译**

Run: `go build ./internal/server/biz/...`
Expected: 编译通过。
---

## Task 3: GraphQL schema 暴露全局默认值字段

**Files:**
- Modify: `internal/server/gql/system.graphql:131`(type RetryPolicy)
- Modify: `internal/server/gql/system.graphql:164`(input UpdateRetryPolicyInput)
- Regenerate: `internal/server/gql/generated.go`

- [ ] **Step 1: 在 `internal/server/gql/system.graphql` 的 `type RetryPolicy`(`emptyResponseDetection: Boolean!` 之后)加字段**

```graphql
  streamInterruptionDefault: String!
```

- [ ] **Step 2: 在 `input UpdateRetryPolicyInput`(`emptyResponseDetection: Boolean` 之后)加字段**

```graphql
  streamInterruptionDefault: String
```

- [ ] **Step 3: 运行 GraphQL 代码生成**

Run: `go generate ./internal/server/gql/...`
Expected: `internal/server/gql/generated.go` 重新生成，包含 `streamInterruptionDefault`。

- [ ] **Step 4: 验证编译**

Run: `go build ./internal/server/gql/...`
Expected: 编译通过。
---

## Task 4: 新增 pipeline 错误与策略类型

**Files:**
- Modify: `llm/pipeline/stream.go`(加 `ErrStreamInterrupted`)
- Modify: `llm/pipeline/pipeline.go`(加策略类型、`StreamInterruptionResolver` 接口)

**架构说明(关键)：** 策略**不能**作为静态 pipeline option 注入。原因：渠道候选选择发生在 `pipe.Process` 内部的 middleware 阶段(`selectCandidates`)，而 pipeline option 在 `orchestrator.go:210` 装配时 candidate 尚未选定(`state.CurrentCandidate == nil`)。所以有效策略(渠道覆盖 > 全局默认，pass-through 降级)必须在**运行时**、candidate 已选之后解析。

方案：定义一个可选接口 `StreamInterruptionResolver`，由 `PersistentOutboundTransformer` 实现(它在运行时持有 `state`、能拿到当前 channel 的 `Settings` 与 `state.RetryPolicyProvider`)。pipeline 的 `streamForPolicy` 通过 `p.Outbound.(StreamInterruptionResolver)` 类型断言拿有效策略。这把策略解析放在 outbound(candidate 已选的运行时点)，pipeline 只消费解析结果。

- [ ] **Step 1: 在 `llm/pipeline/stream.go` 顶部的 `var` 块(与现有 `ErrStream*`/`ErrEmpty*` 错误变量并列)新增错误变量**

Run: `grep -n "^var Err\|ErrStream\|ErrEmpty" llm/pipeline/stream.go llm/pipeline/non_streaming.go | head` 定位现有错误变量块位置，按相同风格加入。

```go
// ErrStreamInterrupted is returned when an upstream stream ends abnormally
// (EOF or transport error) without the protocol's standard termination signal.
// It is retryable: it surfaces from bufferAndReplayStream before any byte is
// sent to the client, so the existing retry flow can switch channels.
var ErrStreamInterrupted = errors.New("upstream stream ended without standard termination signal")
```

`errors` 包需在 stream.go 已 import(应已存在；若无则补 `"errors"`)。

- [ ] **Step 2: 在 `llm/pipeline/pipeline.go` 的 `WithResponseTimeouts` 函数(约 line 92)之后，新增策略类型与 resolver 接口**

```go
// StreamInterruptionPolicy controls the upstream stream-interruption recovery policy.
// This is the llm-pipeline-level mirror of objects.StreamInterruptionPolicy;
// the effective value is resolved at runtime by the Outbound transformer
// (StreamInterruptionResolver) once a channel candidate is selected, so it is
// not a static pipeline Option.
type StreamInterruptionPolicy string

const (
	StreamInterruptionNone       StreamInterruptionPolicy = "none"
	StreamInterruptionComplete   StreamInterruptionPolicy = "complete"
	StreamInterruptionFakeStream StreamInterruptionPolicy = "fakeStream"
)

// StreamInterruptionResolver is an optional interface implemented by the
// Outbound transformer to resolve the effective stream-interruption policy at
// runtime (channel override > global default; forced to "none" when the
// channel uses body pass-through, since fakeStream/complete require the
// transform pipeline). Implementations must be safe to call from
// processRequest, i.e. after candidate selection has run.
type StreamInterruptionResolver interface {
	ResolveStreamInterruption(ctx context.Context) StreamInterruptionPolicy
}
```

- [ ] **Step 3: 验证 llm module 编译**

Run: `cd llm && go build ./pipeline/...`
Expected: 编译通过。
---

## Task 5: 实现 `replayStream` 切片回放流

**Files:**
- Create: `llm/pipeline/replay_stream.go`
- Test: `llm/pipeline/replay_stream_test.go`

- [ ] **Step 1: 写测试 `llm/pipeline/replay_stream_test.go`**

```go
package pipeline

import (
	"testing"

	"github.com/looplj/axonhub/llm/httpclient"
)

func TestReplayStream_YieldsAllEventsThenEnds(t *testing.T) {
	events := []*httpclient.StreamEvent{
		{Data: []byte(`{"a":1}`)},
		{Data: []byte(`{"a":2}`)},
		{Data: []byte("[DONE]")},
	}
	s := newReplayStream(events)

	got := 0
	for i := 0; i < len(events); i++ {
		if !s.Next() {
			t.Fatalf("Next() returned false at index %d before all events yielded", i)
		}
		got++
		cur := s.Current()
		if cur == nil || string(cur.Data) != string(events[i].Data) {
			t.Fatalf("Current() mismatch at %d: got %v want %v", i, cur, events[i])
		}
	}
	if s.Next() {
		t.Fatalf("Next() should return false after all events yielded")
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() should be nil, got %v", err)
	}
	if got != len(events) {
		t.Fatalf("yielded %d events, want %d", got, len(events))
	}
}

func TestReplayStream_EmptyEventsEndsImmediately(t *testing.T) {
	s := newReplayStream(nil)
	if s.Next() {
		t.Fatalf("Next() should return false for empty replay")
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() should be nil, got %v", err)
	}
}

func TestReplayStream_CloseIsIdempotent(t *testing.T) {
	s := newReplayStream([]*httpclient.StreamEvent{{Data: []byte("x")}})
	if err := s.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close() returned error: %v", err)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd llm && go test ./pipeline/ -run TestReplayStream -v`
Expected: FAIL with `undefined: newReplayStream`。

- [ ] **Step 3: 实现 `llm/pipeline/replay_stream.go`**

```go
package pipeline

import (
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

// replayStream is a streams.Stream that replays a fixed slice of pre-buffered
// events in order, then signals end. It is used by bufferAndReplayStream to
// feed the buffered upstream chunks back to the client as an SSE stream after
// the full upstream stream has been verified complete.
type replayStream struct {
	events []*httpclient.StreamEvent
	index  int
	closed bool
}

func newReplayStream(events []*httpclient.StreamEvent) *replayStream {
	return &replayStream{events: events}
}

func (s *replayStream) Next() bool {
	if s.index >= len(s.events) {
		return false
	}
	s.index++
	return true
}

func (s *replayStream) Current() *httpclient.StreamEvent {
	if s.index <= 0 || s.index > len(s.events) {
		return nil
	}
	return s.events[s.index-1]
}

func (s *replayStream) Err() error {
	return nil
}

func (s *replayStream) Close() error {
	s.closed = true
	return nil
}

var _ streams.Stream[*httpclient.StreamEvent] = (*replayStream)(nil)
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd llm && go test ./pipeline/ -run TestReplayStream -v`
Expected: PASS (3 tests)。
---

## Task 6: 实现 `bufferAndReplayStream`(模式 3 核心)与完整性判定

**Files:**
- Modify: `llm/pipeline/non_streaming.go`(文件末尾加 `bufferAndReplayStream`、`streamEndedNormally`、`hasFinishReasonInRaw`)
- Test: `llm/pipeline/buffer_replay_test.go`(Create)

- [ ] **Step 1: 在 `llm/pipeline/non_streaming.go` 末尾实现 `bufferAndReplayStream`、`streamEndedNormally`、`hasFinishReasonInRaw`**

```go
// bufferAndReplayStream implements the "fakeStream" interruption policy.
// It initiates an upstream stream, buffers the entire response server-side,
// verifies the stream ended with the protocol's standard termination signal,
// and only then returns a replayStream that feeds the buffered chunks to the
// client as SSE. If the upstream stream ends abnormally (EOF/transport error
// without a termination signal), it returns ErrStreamInterrupted — which
// surfaces before any byte is sent to the client, so the existing retry flow
// in pipeline.Process can switch channels transparently.
func (p *pipeline) bufferAndReplayStream(
	ctx context.Context,
	executor Executor,
	request *httpclient.Request,
) (*Result, error) {
	inboundStream, err := p.stream(ctx, executor, request, 0)
	if err != nil {
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
		p.applyRawErrorResponseMiddlewares(ctx, err)
		return nil, err
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
		EventStream: newReplayStream(chunks),
	}, nil
}

// streamEndedNormally checks whether the buffered chunk sequence contains the
// protocol's standard termination signal. bufferAndReplayStream operates on
// already-inbound-transformed httpclient.StreamEvent (the final SSE bytes), so
// it inspects the raw payload for the [DONE] sentinel and for finish_reason /
// stop_reason fields.
func streamEndedNormally(chunks []*httpclient.StreamEvent) bool {
	for _, c := range chunks {
		if c == nil || len(c.Data) == 0 {
			continue
		}
		if string(c.Data) == "[DONE]" {
			return true
		}
		if hasFinishReasonInRaw(c.Data) {
			return true
		}
	}
	return false
}

// hasFinishReasonInRaw reports whether the raw SSE data bytes contain a
// finish_reason / stop_reason field with a non-null value, indicating the
// protocol's standard termination signal. Coarse substring check avoids full
// JSON parsing per chunk.
func hasFinishReasonInRaw(data []byte) bool {
	s := string(data)
	if hasNonNullOrAfter(s, "finish_reason") {
		return true
	}
	if hasNonNullOrAfter(s, "stop_reason") {
		return true
	}
	return false
}

// hasNonNullOrAfter reports whether the field named by key appears in s
// followed by a non-null value. It skips the optional whitespace/colon/quote
// between the key and the value, then checks the value does not start with 'n'
// (the leading char of JSON null).
func hasNonNullOrAfter(s, key string) bool {
	idx := strings.Index(s, key)
	if idx < 0 {
		return false
	}
	rest := s[idx+len(key):]
	for len(rest) > 0 {
		ch := rest[0]
		if ch == ' ' || ch == ':' || ch == '"' || ch == '\t' || ch == '\n' {
			rest = rest[1:]
			continue
		}
		break
	}
	return len(rest) > 0 && rest[0] != 'n'
}
```

`strings` 包需在 `non_streaming.go` import 块补 `"strings"`(若未引用)。

- [ ] **Step 2: 写测试 `llm/pipeline/buffer_replay_test.go`**

复用 `pipeline_retry_test.go` 的 `mockExecutor`/`mockOutbound`/`mockInbound` 模式(定义在 `llm/pipeline/pipeline_retry_test.go:17/56/136`)。`mockInbound` 有 `transformStream` 字段(`:21`)。策略经 `StreamInterruptionResolver` 动态解析(见 Task 4/8),所以测试用一个实现该接口的 mock Outbound。`bufferAndReplayStream` 内部调 `p.stream(...)`，走 outbound.TransformStream → inbound.TransformStream 全链路。为了让 inbound 产出可被 `streamEndedNormally` 识别的终止信号，mockOutbound 的 transformStream 要产出带 FinishReason 的 llm.Response 或 `llm.DoneResponse`，mockInbound 的 transformStream 把它转成带 `finish_reason`/`[DONE]` 的 httpclient.StreamEvent。

```go
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

	// processRequest 入参是 *llm.Request(不是 *httpclient.Request)；Stream 标志在 llm.Request 上。
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

// TestStreamEndedNormally: 单元测试完整性判定。
func TestStreamEndedNormally(t *testing.T) {
	require.True(t, streamEndedNormally([]*httpclient.StreamEvent{{Data: []byte("[DONE]")}}))
	require.True(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)},
	}))
	require.True(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)},
	}))
	require.False(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{"content":"hi"}}]}`)},
	}))
	require.False(t, streamEndedNormally([]*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{},"finish_reason":null}]}`)},
	}))
	require.False(t, streamEndedNormally(nil))
}
```

> **已核实：** `mockInbound`/`mockOutbound`/`mockExecutor` 定义在 `llm/pipeline/pipeline_retry_test.go:17/56/136`，字段名 `transformStream`/`transformRequest`/`doStream` 等与上面一致。`streams.MapErr[T,R](stream, mapper func(T)(R,error)) Stream[R]`(`llm/streams/map.go:31`)。`processRequest` 入参是 `*llm.Request`(`pipeline.go:372`)，`llm.Request.Stream` 是 `*bool`，**不要**给 `httpclient.Request` 设 Stream(`httpclient.Request.Stream` 是 `io.ReadCloser`，`model.go:94`)。`mockOutbound` 是 struct(非接口)，可被 `fakeStreamResolver` 嵌入以实现 `StreamInterruptionResolver`。

- [ ] **Step 3: 运行测试，确认通过**

Run: `cd llm && go test ./pipeline/ -run "TestBufferAndReplayStream|TestStreamEndedNormally" -v`
Expected: PASS (3 tests)。若 mock 字段名不匹配，按 Step 2 注释调整后重跑。
---

## Task 7: 实现 `completingStream`(模式 2 补齐)

**Files:**
- Modify: `llm/transformer/interfaces.go`(新增 `StreamCompleter` 接口)
- Modify: `llm/pipeline/stream.go`(新增 `completingStream`)
- Test: `llm/pipeline/completing_stream_test.go`(Create)

- [ ] **Step 1: 在 `llm/transformer/interfaces.go` 末尾新增可选接口**

```go
// StreamCompleter is an optional interface for inbound transformers that can
// synthesize the protocol's standard termination events when an upstream stream
// ends abnormally (mid-stream drop without a termination signal). This is used
// by the "complete" stream-interruption policy so the client ends cleanly.
//
// interruptedUsage is the best-effort output-token count accumulated from the
// stream so far; transformers should fill it into the synthesized termination
// event's usage field when the protocol supports one, or omit usage if unknown.
type StreamCompleter interface {
	// CompletionEvents returns the protocol's standard termination events to
	// append when the stream ended without one. For OpenAI this is a chunk with
	// finish_reason="length" plus a [DONE]; for Anthropic this is a message_delta
	// with stop_reason="max_tokens" and a message_stop; for Gemini this is a
	// response with candidates[].finishReason="MAX_TOKENS". The slice may be
	// empty if the transformer has nothing to add.
	CompletionEvents(ctx context.Context, interruptedUsage *int) []*httpclient.StreamEvent
}
```

- [ ] **Step 2: 写测试 `llm/pipeline/completing_stream_test.go`**

```go
package pipeline

import (
	"context"
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

func (f *fakeCompleter) CompletionEvents(_ context.Context, _ *int) []*httpclient.StreamEvent {
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
	s := newCompletingStream(inner, completer, nil)

	var got [][]byte
	for s.Next() {
		got = append(got, s.Current().Data)
	}
	require := func(cond bool, msg string) {
		if !cond {
			t.Fatal(msg)
		}
	}
	require(s.Err() == nil, "Err() should be nil")
	require(len(got) == 4, "got %d events, want 4")
	require(string(got[3]) == "[DONE]", "last event = %s, want [DONE]", got[3])
}

func TestCompletingStream_NoAppendWhenTerminationPresent(t *testing.T) {
	inner := &fakeCompleteInner{events: []*httpclient.StreamEvent{
		{Data: []byte(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)},
		{Data: []byte("[DONE]")},
	}}
	completer := &fakeCompleter{extra: []*httpclient.StreamEvent{
		{Data: []byte("SHOULD_NOT_APPEAR")},
	}}
	s := newCompletingStream(inner, completer, nil)

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

func TestCompletingStream_NoAppendWhenInnerErr(t *testing.T) {
	inner := &errInner{}
	completer := &fakeCompleter{extra: []*httpclient.StreamEvent{{Data: []byte("X")}}}
	s := newCompletingStream(inner, completer, nil)
	for s.Next() {
		t.Fatalf("should not yield when inner errored")
	}
}

type errInner struct{}

func (e *errInner) Next() bool                                { return false }
func (e *errInner) Current() *httpclient.StreamEvent          { return nil }
func (e *errInner) Err() error                                { return errInnerErr }
func (e *errInner) Close() error                              { return nil }

var errInnerErr = newSentinelErr("inner error")

func newSentinelErr(msg string) error { return &sentinelErr{msg: msg} }

type sentinelErr struct{ msg string }

func (e *sentinelErr) Error() string { return e.msg }

// keep streams import used
var _ streams.Stream[*httpclient.StreamEvent] = (*fakeCompleteInner)(nil)
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `cd llm && go test ./pipeline/ -run TestCompletingStream -v`
Expected: FAIL with `undefined: newCompletingStream`。

- [ ] **Step 4: 实现 `completingStream`，加在 `llm/pipeline/stream.go` 末尾**

采用 phase/index 形态(最清晰)：phase 0 = 透传 inner，phase 1 = 回放补齐事件。

```go
// completingStream wraps an inbound SSE stream and, when the upstream stream
// ends without the protocol's standard termination signal, appends the
// termination events produced by the inbound transformer's StreamCompleter.
// This is the "complete" stream-interruption policy: the client ends cleanly
// (no error, no retry) but sees a length-capped (max_tokens/length) stop reason
// rather than the true mid-stream-drop cause.
type completingStream struct {
	inner            streams.Stream[*httpclient.StreamEvent]
	completer        transformer.StreamCompleter
	interruptedUsage *int

	sawTermination bool
	phase          int // 0 = draining inner, 1 = yielding appended events
	appended       []*httpclient.StreamEvent
	appendedIdx    int
}

func newCompletingStream(
	inner streams.Stream[*httpclient.StreamEvent],
	completer transformer.StreamCompleter,
	interruptedUsage *int,
) *completingStream {
	return &completingStream{
		inner:            inner,
		completer:        completer,
		interruptedUsage: interruptedUsage,
	}
}

func (s *completingStream) Next() bool {
	if s.phase == 0 {
		if s.inner.Next() {
			if cur := s.inner.Current(); cur != nil && hasFinishReasonInRaw(cur.Data) {
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
		s.appended = s.completer.CompletionEvents(context.Background(), s.interruptedUsage)
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
```

`transformer` 包需在 stream.go import 块确认已引用(应已存在；pipeline.go 已 import `github.com/looplj/axonhub/llm/transformer`)。

- [ ] **Step 5: 运行测试，确认通过**

Run: `cd llm && go test ./pipeline/ -run TestCompletingStream -v`
Expected: PASS (3 tests)。

- [ ] **Step 6: 验证 llm module 全量测试**

Run: `cd llm && go test ./pipeline/...`
Expected: 全部 PASS。
---

## Task 8: `processRequest` 接入策略分支

**Files:**
- Modify: `llm/pipeline/pipeline.go`(processRequest 的 `case originalWantStream:` 分支 + 新增 `streamForPolicy`/`wrapWithCompletingStream`)

- [ ] **Step 1: 修改 `processRequest`(`llm/pipeline/pipeline.go:402-414`)的 `case originalWantStream:` 分支**

把：
```go
	case originalWantStream:
		result = &Result{
			Stream: true,
		}

		stream, err := p.stream(ctx, executor, httpReq, p.streamFirstEventTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to stream request: %w", err)
		}

		result.EventStream = stream
```
改为：
```go
	case originalWantStream:
		result = &Result{
			Stream: true,
		}

		stream, err := p.streamForPolicy(ctx, executor, httpReq)
		if err != nil {
			return nil, fmt.Errorf("failed to stream request: %w", err)
		}

		result.EventStream = stream
```

- [ ] **Step 2: 在 `llm/pipeline/pipeline.go` 的 `processRequest` 之后新增 `streamForPolicy` 与 `wrapWithCompletingStream`**

策略从 `p.Outbound` 的 `StreamInterruptionResolver` 动态解析(运行时 candidate 已选)。若 Outbound 未实现该接口，回退 `StreamInterruptionNone`(= 现状行为)。

```go
// streamForPolicy dispatches the streaming request according to the effective
// stream-interruption policy, resolved at runtime from the Outbound transformer.
func (p *pipeline) streamForPolicy(
	ctx context.Context,
	executor Executor,
	httpReq *httpclient.Request,
) (streams.Stream[*httpclient.StreamEvent], error) {
	policy := StreamInterruptionNone
	if resolver, ok := p.Outbound.(StreamInterruptionResolver); ok {
		policy = resolver.ResolveStreamInterruption(ctx)
	}

	switch policy {
	case StreamInterruptionFakeStream:
		res, err := p.bufferAndReplayStream(ctx, executor, httpReq)
		if err != nil {
			return nil, err
		}
		return res.EventStream, nil
	case StreamInterruptionComplete:
		stream, err := p.stream(ctx, executor, httpReq, p.streamFirstEventTimeout)
		if err != nil {
			return nil, err
		}
		return p.wrapWithCompletingStream(stream), nil
	default: // StreamInterruptionNone
		return p.stream(ctx, executor, httpReq, p.streamFirstEventTimeout)
	}
}

// wrapWithCompletingStream wraps an inbound stream with a completingStream if
// the inbound transformer implements StreamCompleter; otherwise returns the
// stream unchanged (complete-policy is a no-op when the inbound format cannot
// synthesize termination events).
func (p *pipeline) wrapWithCompletingStream(stream streams.Stream[*httpclient.StreamEvent]) streams.Stream[*httpclient.StreamEvent] {
	if completer, ok := p.Inbound.(transformer.StreamCompleter); ok {
		return newCompletingStream(stream, completer, nil)
	}
	return stream
}
```

- [ ] **Step 3: 验证编译**

Run: `cd llm && go build ./pipeline/...`
Expected: 编译通过。

- [ ] **Step 4: 运行 pipeline 全量测试，确保未破坏现有流式行为**

Run: `cd llm && go test ./pipeline/...`
Expected: 全部 PASS。现有测试的 mockOutbound 未实现 `StreamInterruptionResolver` → `streamForPolicy` 回退 `StreamInterruptionNone` → 走原 `p.stream` 行为，与改动前等价。
---

## Task 9: `PersistentOutboundTransformer` 实现 `StreamInterruptionResolver`

**Files:**
- Modify: `internal/server/orchestrator/outbound.go`(或 `pass_through.go`，与现有 channel 设置访问逻辑放一起)

**架构(已核实)：** `PersistentOutboundTransformer` 持有 `state *PersistenceState`(`outbound.go:317`)，`state.RetryPolicyProvider`(`state.go:22`)是 `RetryPolicyProvider` 接口(`load_balancer.go:82`)，其 `RetryPolicyOrDefault(ctx) *biz.RetryPolicy` 方法可拿到全局默认(含 `StreamInterruptionDefault`)。`GetCurrentChannel() *biz.Channel`(`outbound.go:482`)在运行时(candidate 已选)返回当前渠道，`channel.Settings` 是 `*objects.ChannelSettings`(含 `StreamInterruption *StreamInterruptionPolicy` 与 `PassThroughBody *bool`)。

`ResolveStreamInterruption` 在 `processRequest` 内被 `streamForPolicy` 调用(此时 `applyRawRequestMiddlewares` 已执行、candidate 已选)，与 `isPassThroughEnabled` 同属运行时点。

- [ ] **Step 1: 在 `internal/server/orchestrator/outbound.go`(或 `pass_through.go`)为 `PersistentOutboundTransformer` 实现 `ResolveStreamInterruption`**

```go
// ResolveStreamInterruption resolves the effective stream-interruption policy
// for the currently selected channel: channel override wins over the global
// default. When the channel enables body pass-through, fakeStream/complete are
// unusable (they require the transform pipeline), so the policy is forced to
// "none".
func (p *PersistentOutboundTransformer) ResolveStreamInterruption(ctx context.Context) pipeline.StreamInterruptionPolicy {
	// Global default from RetryPolicy.
	policy := pipeline.StreamInterruptionNone
	if p.state != nil && p.state.RetryPolicyProvider != nil {
		rp := p.state.RetryPolicyProvider.RetryPolicyOrDefault(ctx)
		if rp != nil {
			policy = pipeline.StreamInterruptionPolicy(rp.StreamInterruptionDefault)
		}
	}

	// Channel override wins.
	channel := p.GetCurrentChannel()
	if channel != nil && channel.Settings != nil {
		if channel.Settings.StreamInterruption != nil {
			policy = pipeline.StreamInterruptionPolicy(*channel.Settings.StreamInterruption)
		}
		// Pass-through forces "none": fakeStream buffering and complete-event
		// synthesis both require the transform pipeline, which pass-through skips.
		if channel.Settings.PassThroughBody != nil && *channel.Settings.PassThroughBody {
			policy = pipeline.StreamInterruptionNone
		}
	}

	if policy == "" {
		policy = pipeline.StreamInterruptionFakeStream
	}
	return policy
}
```

`pipeline` 包需在 outbound.go import(确认 `internal/server/orchestrator` 已 import `github.com/looplj/axonhub/llm/pipeline`——`pass_through.go` 已 import，同包共享；若 outbound.go 未 import 则补)。

> **已核实：** `pipeline.StreamInterruptionPolicy` 是 `string` 别名(Task 4 定义)，可与 `objects.StreamInterruptionPolicy`(`string` 别名，Task 1 定义)直接互转。`biz.RetryPolicy.StreamInterruptionDefault` 类型是 `objects.StreamInterruptionPolicy`(Task 2 定义)，赋给 `pipeline.StreamInterruptionPolicy` 需显式转换 `pipeline.StreamInterruptionPolicy(rp.StreamInterruptionDefault)`——已在上代码中转换。`channel.Settings.StreamInterruption` 是 `*objects.StreamInterruptionPolicy`，`*channel.Settings.StreamInterruption` 解引用为 `objects.StreamInterruptionPolicy`，再转 `pipeline.StreamInterruptionPolicy`——已转换。

- [ ] **Step 2: 编译期接口实现校验**

在 `outbound.go`(或 `pass_through.go`)末尾加编译期断言：

```go
var _ pipeline.StreamInterruptionResolver = (*PersistentOutboundTransformer)(nil)
```

- [ ] **Step 3: 验证编译**

Run: `go build ./internal/server/orchestrator/...`
Expected: 编译通过。

- [ ] **Step 4: 写测试 `internal/server/orchestrator/stream_interruption_resolver_test.go`**

参考 `pass_through_test.go` 或 `orchestrator_basic_test.go` 的 mock 构造(`SetSettings(&objects.ChannelSettings{...})`)：

```go
package orchestrator

import (
	"context"
	"testing"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/pipeline"
)

func TestPersistentOutboundTransformer_ResolveStreamInterruption(t *testing.T) {
	ctx := context.Background()

	t.Run("channel override wins over global default", func(t *testing.T) {
		outbound := newTestOutboundWithSettings(t, &objects.ChannelSettings{
			StreamInterruption: lo.ToPtr(objects.StreamInterruptionComplete),
		})
		require.Equal(t, pipeline.StreamInterruptionComplete, outbound.ResolveStreamInterruption(ctx))
	})

	t.Run("falls back to global default when channel nil", func(t *testing.T) {
		outbound := newTestOutboundWithSettings(t, nil)
		// global default is fakeStream (system_default.go)
		require.Equal(t, pipeline.StreamInterruptionFakeStream, outbound.ResolveStreamInterruption(ctx))
	})

	t.Run("pass-through forces none", func(t *testing.T) {
		outbound := newTestOutboundWithSettings(t, &objects.ChannelSettings{
			StreamInterruption: lo.ToPtr(objects.StreamInterruptionFakeStream),
			PassThroughBody:    lo.ToPtr(true),
		})
		require.Equal(t, pipeline.StreamInterruptionNone, outbound.ResolveStreamInterruption(ctx))
	})
}
```

`newTestOutboundWithSettings` 是测试 helper：构造一个 `PersistentOutboundTransformer`，其 `GetCurrentChannel()` 返回带指定 settings 的 `*biz.Channel`，`state.RetryPolicyProvider` 用真实的 `biz.SystemService` 或返回 `&biz.RetryPolicy{StreamInterruptionDefault: objects.StreamInterruptionFakeStream}` 的 stub。**执行时参考 `pass_through_test.go` 里构造 outbound + channel + systemService stub 的现有 helper**(Run `grep -n "func newTest\|func setup\|SystemService\|RetryPolicyOrDefault" internal/server/orchestrator/pass_through_test.go`)，复用其构造方式实现 `newTestOutboundWithSettings`。

- [ ] **Step 5: 运行测试**

Run: `go test ./internal/server/orchestrator/ -run TestPersistentOutboundTransformer_ResolveStreamInterruption -v`
Expected: PASS (3 subtests)。

---

## Task 10: 三个协议 inbound 实现 `StreamCompleter`

覆盖 OpenAI、Anthropic、Gemini 全部三个协议。

**Files:**
- Modify: `llm/transformer/openai/inbound.go`
- Modify: `llm/transformer/anthropic/inbound.go`
- Test: 各 transformer 下的 `*_test.go`

### Task 10a: OpenAI inbound

- [ ] **Step 1: 在 `llm/transformer/openai/inbound.go` 的 `InboundTransformer` 上实现 `CompletionEvents`**

复用现有 `TransformStreamChunk`：构造一个带 `finish_reason:"length"` 的 `*llm.Response` 调它得到终止 chunk，再调一次 `llm.DoneResponse` 得 `[DONE]`。

```go
// CompletionEvents synthesizes the OpenAI standard termination events for an
// abnormally-ended stream: a chunk with finish_reason="length" (honest
// "truncated" semantics) followed by [DONE].
func (t *InboundTransformer) CompletionEvents(ctx context.Context, interruptedUsage *int) []*httpclient.StreamEvent {
	length := "length"
	terminalResp := &llm.Response{
		Choices: []llm.Choice{{
			Index:         0,
			FinishReason:  &length,
			Delta:         &llm.Message{},
		}},
	}
	if interruptedUsage != nil {
		terminalResp.Usage = &llm.Usage{CompletionTokens: int64(*interruptedUsage)}
	}
	chunk, err := t.TransformStreamChunk(ctx, terminalResp)
	if err != nil || chunk == nil {
		// fallback: bare [DONE]
		return []*httpclient.StreamEvent{{Data: []byte("[DONE]")}}
	}
	done, _ := t.TransformStreamChunk(ctx, llm.DoneResponse)
	if done == nil {
		done = &httpclient.StreamEvent{Data: []byte("[DONE]")}
	}
	return []*httpclient.StreamEvent{chunk, done}
}
```

> **执行确认：** `llm.Choice`/`llm.Message`/`llm.Usage` 的字段名以 `llm/model.go` 为准(Run `grep -n "type Choice struct\|type Message struct\|type Usage struct" llm/model.go`)。`CompletionTokens` 字段名以实际为准(可能是 `CompletionTokens` 或 `OutputTokens`)。`TransformStreamChunk` 对带 FinishReason 的 Response 会走 `ResponseFromLLM` 序列化(`inbound.go:134`)，产出含 `"finish_reason":"length"` 的 chunk —— 符合预期。

- [ ] **Step 2: 写测试 `llm/transformer/openai/inbound_stream_interruption_test.go`**

```go
package openai

import (
	"context"
	"strings"
	"testing"

	"github.com/looplj/axonhub/llm"
)

func TestInboundTransformer_CompletionEvents(t *testing.T) {
	tt := &InboundTransformer{}
	events := tt.CompletionEvents(context.Background(), nil)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if string(events[1].Data) != "[DONE]" {
		t.Fatalf("second event = %s, want [DONE]", events[1].Data)
	}
	if !strings.Contains(string(events[0].Data), `"finish_reason":"length"`) {
		t.Fatalf("first event = %s, want finish_reason:length", events[0].Data)
	}
}

// keep llm import used
var _ = llm.DoneResponse
```

- [ ] **Step 3: 运行测试**

Run: `cd llm && go test ./transformer/openai/ -run TestInboundTransformer_CompletionEvents -v`
Expected: PASS。若 `finish_reason` 字段名/序列化不匹配，按 `ResponseFromLLM` 实际产出调整断言。
### Task 10b: Anthropic inbound

- [ ] **Step 5: 在 `llm/transformer/anthropic/inbound.go` 的 `InboundTransformer` 上实现 `CompletionEvents`**

Anthropic 无 `TransformStreamChunk`，直接构造 `StreamEvent` struct 后 `json.Marshal`(参考 `inbound_stream.go:868-895` 的 message_delta/message_stop 构造)。`StreamEvent.Usage` 类型是 `*Usage`(`model.go:466`)，用现成的 `convertToAnthropicUsage(*llm.Usage) *Usage`(`usage.go:91`)转换，不手写 `Usage{}`。`StreamDelta.StopReason` 是 `*string`(`model.go:494`)，用取地址避免引入 `lo` 依赖(`inbound.go` 未 import `lo`)。

```go
// CompletionEvents synthesizes the Anthropic standard termination events for
// an abnormally-ended stream: message_delta with stop_reason="max_tokens" +
// usage, then message_stop.
func (t *InboundTransformer) CompletionEvents(ctx context.Context, interruptedUsage *int) []*httpclient.StreamEvent {
	stopReason := "max_tokens"
	delta := StreamEvent{
		Type:  "message_delta",
		Delta: &StreamDelta{StopReason: &stopReason},
	}
	if interruptedUsage != nil {
		delta.Usage = convertToAnthropicUsage(&llm.Usage{CompletionTokens: int64(*interruptedUsage)})
	}
	deltaData, err := json.Marshal(delta)
	if err != nil {
		return []*httpclient.StreamEvent{{Data: []byte(`{"type":"message_stop"}`)}}
	}
	stopData, _ := json.Marshal(StreamEvent{Type: "message_stop"})
	return []*httpclient.StreamEvent{
		{Data: deltaData},
		{Data: stopData},
	}
}
```

`inbound.go` 已 import `llm`、`encoding/json`，无需新增 import。`convertToAnthropicUsage` 是同包函数(`usage.go:91`)，直接可用。

- [ ] **Step 6: 写测试 `llm/transformer/anthropic/inbound_stream_interruption_test.go`**

```go
package anthropic

import (
	"context"
	"strings"
	"testing"
)

func TestInboundTransformer_CompletionEvents(t *testing.T) {
	tt := &InboundTransformer{}
	events := tt.CompletionEvents(context.Background(), nil)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if !strings.Contains(string(events[0].Data), `"stop_reason":"max_tokens"`) {
		t.Fatalf("first event = %s, want stop_reason:max_tokens", events[0].Data)
	}
	if !strings.Contains(string(events[0].Data), `"message_delta"`) {
		t.Fatalf("first event = %s, want type message_delta", events[0].Data)
	}
	if !strings.Contains(string(events[1].Data), `"message_stop"`) {
		t.Fatalf("second event = %s, want message_stop", events[1].Data)
	}
}
```

- [ ] **Step 7: 运行测试**

Run: `cd llm && go test ./transformer/anthropic/ -run TestInboundTransformer_CompletionEvents -v`
Expected: PASS。
### Task 10c: Gemini inbound

- [ ] **Step 9: 在 `llm/transformer/gemini/inbound.go` 的 `InboundTransformer` 上实现 `CompletionEvents`**

Gemini 终止靠 `candidates[].finishReason="MAX_TOKENS"`(Gemini 的 finishReason 枚举值是大写下划线，见 `convert.go:94` 的 `convertGeminiFinishReasonToLLM`)。复用 `TransformStreamChunk`：构造带 FinishReason 的 `*llm.Response`，由 `convertLLMToGeminiResponse` 转成含 `finishReason` 的 Gemini chunk。

```go
// CompletionEvents synthesizes the Gemini standard termination events for an
// abnormally-ended stream: a response with candidates[].finishReason="MAX_TOKENS".
// Gemini does not use a [DONE] sentinel; termination is signaled by finishReason.
func (t *InboundTransformer) CompletionEvents(ctx context.Context, interruptedUsage *int) []*httpclient.StreamEvent {
	length := "length"
	terminalResp := &llm.Response{
		Choices: []llm.Choice{{
			Index:        0,
			FinishReason: &length,
			Delta:        &llm.Message{},
		}},
	}
	if interruptedUsage != nil {
		terminalResp.Usage = &llm.Usage{CompletionTokens: int64(*interruptedUsage)}
	}
	chunk, err := t.TransformStreamChunk(ctx, terminalResp)
	if err != nil || chunk == nil {
		return nil
	}
	return []*httpclient.StreamEvent{chunk}
}
```

> **执行确认：** `convertLLMFinishReasonToGemini("length")` 应映射到 `"MAX_TOKENS"`(Run `grep -n "MAX_TOKENS\|length" llm/transformer/gemini/convert.go`)。若 `TransformStreamChunk` 对纯 FinishReason(无 content)的 Response 不产出 chunk(参考 `inbound_stream.go:302` 的 `hasNonToolContent || len(completed) > 0 || outChoice.FinishReason != nil` —— FinishReason 非空会 emit)，则 chunk 非空。若 `TransformStreamChunk` 内部对 `[DONE]` 返回 nil 而 Gemini 不需要 `[DONE]`，这里只返回一个 chunk 即可。

- [ ] **Step 10: 写测试 `llm/transformer/gemini/inbound_stream_interruption_test.go`**

```go
package gemini

import (
	"context"
	"strings"
	"testing"
)

func TestInboundTransformer_CompletionEvents(t *testing.T) {
	tt := &InboundTransformer{}
	events := tt.CompletionEvents(context.Background(), nil)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if !strings.Contains(string(events[0].Data), `"finishReason":"MAX_TOKENS"`) {
		t.Fatalf("event = %s, want finishReason:MAX_TOKENS", events[0].Data)
	}
}
```

- [ ] **Step 11: 运行测试**

Run: `cd llm && go test ./transformer/gemini/ -run TestInboundTransformer_CompletionEvents -v`
Expected: PASS。若 `finishReason` 序列化不匹配(如字段名是 `finishReason` 还是 `finish_reason`、值是 `MAX_TOKENS` 还是 `length`)，按 `convertLLMToGeminiResponse` 实际产出调整断言。

- [ ] **Step 12: 验证三个协议 transformer 全量测试**

Run: `cd llm && go test ./transformer/openai/... ./transformer/anthropic/... ./transformer/gemini/...`
Expected: 全部 PASS。
---

## Task 11: 前端 — channel schema 与 GraphQL 查询

**Files:**
- Modify: `frontend/src/features/channels/data/schema.ts:219`
- Modify: 前端 GraphQL channel 查询(含 `passThroughBody` 的 fragment)

- [ ] **Step 1: 在 `frontend/src/features/channels/data/schema.ts` 的 ChannelSettings schema(`passThroughBody` 字段附近)加字段**

```ts
  streamInterruption: z.string().optional().nullable(),
```

- [ ] **Step 2: 在前端 GraphQL channel 查询的 `settings` selection 里加字段**

Run: `grep -rn "passThroughBody" frontend/src/features/channels/ frontend/src/gql/` 找到查询/fragment 位置，在 `passThroughBody` 同级加：

```graphql
          streamInterruption
```

- [ ] **Step 3: 运行前端 GraphQL codegen**

Run: `grep -n "\"codegen\"\|\"gen\"" frontend/package.json` 找脚本名，执行(典型 `pnpm codegen`)。
Expected: 生成的 types 包含 `streamInterruption?: string | null`。

- [ ] **Step 4: 验证前端类型检查**

Run: `cd frontend && pnpm typecheck`
Expected: 无类型错误。
---

## Task 12: 前端 — 渠道设置 dialog

**Files:**
- Create: `frontend/src/features/channels/components/channels-stream-interruption-dialog.tsx`
- Modify: `frontend/src/features/channels/components/index.ts`
- Modify: `frontend/src/features/channels/components/channels-table.tsx`(或 channels-action-dialog.tsx，按 rate-limit 入口位置)
- Modify: `frontend/src/locales/zh.json`、`en.json`

- [ ] **Step 1: 先查实 rate-limit dialog 的真实 API**

Run:
```bash
sed -n '77,180p' frontend/src/features/channels/components/channels-rate-limit-dialog.tsx
grep -n "mergeChannelSettingsForUpdate" frontend/src/features/channels/utils/merge.ts
grep -n "useUpdateChannel\|mutate" frontend/src/features/channels/data/channels.ts | head
```
记录：`mergeChannelSettingsForUpdate` 的真实签名、`useUpdateChannel().mutate` 的入参形状、`Select` 组件的真实 import 路径(参考其它 dialog)。

- [ ] **Step 2: 创建 `frontend/src/features/channels/components/channels-stream-interruption-dialog.tsx`**

按 Step 1 查实的 API 调整下面代码中的 `mergeChannelSettingsForUpdate` 调用与 `updateChannel.mutate` 入参。

```tsx
'use client';

import { useEffect } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Form, FormField, FormItem, FormLabel, FormMessage, FormControl, FormDescription } from '@/components/ui/form';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Button } from '@/components/ui/button';
import { useUpdateChannel } from '../data/channels';
import { Channel } from '../data/schema';
import { mergeChannelSettingsForUpdate } from '../utils/merge';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  currentRow: Channel;
}

const formSchema = z.object({
  streamInterruption: z.enum(['none', 'complete', 'fakeStream']).or(z.literal('')).nullable().optional(),
});
type FormValues = z.infer<typeof formSchema>;

function valuesFromChannel(row: Channel): FormValues {
  const v = row.settings?.streamInterruption;
  return { streamInterruption: (v ?? '') as FormValues['streamInterruption'] };
}

export function ChannelsStreamInterruptionDialog({ open, onOpenChange, currentRow }: Props) {
  const { t } = useTranslation();
  const updateChannel = useUpdateChannel();
  const form = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: valuesFromChannel(currentRow),
    mode: 'onChange',
  });

  useEffect(() => {
    if (open) form.reset(valuesFromChannel(currentRow));
  }, [open, currentRow, form]);

  const disabled = currentRow.settings?.passThroughBody === true;

  const onSubmit = (values: FormValues) => {
    const next = values.streamInterruption === '' ? null : values.streamInterruption;
    // 按 Step 1 查实的 mergeChannelSettingsForUpdate 签名调用：
    const settings = mergeChannelSettingsForUpdate(currentRow.settings, { streamInterruption: next });
    updateChannel.mutate(
      { id: currentRow.id, settings },
      {
        onSuccess: () => { toast.success(t('common.success.updated')); onOpenChange(false); },
        onError: () => toast.error(t('common.errors.updateFailed')),
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('channels.streamInterruption.title')}</DialogTitle>
          <DialogDescription>{t('channels.streamInterruption.description')}</DialogDescription>
        </DialogHeader>
        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
            <FormField
              control={form.control}
              name="streamInterruption"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('channels.streamInterruption.policy')}</FormLabel>
                  <Select disabled={disabled} value={field.value ?? ''} onValueChange={field.onChange}>
                    <FormControl>
                      <SelectTrigger>
                        <SelectValue placeholder={t('channels.streamInterruption.inheritDefault')} />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent>
                      <SelectItem value="">{t('channels.streamInterruption.inheritDefault')}</SelectItem>
                      <SelectItem value="none">{t('channels.streamInterruption.none')}</SelectItem>
                      <SelectItem value="complete">{t('channels.streamInterruption.complete')}</SelectItem>
                      <SelectItem value="fakeStream">{t('channels.streamInterruption.fakeStream')}</SelectItem>
                    </SelectContent>
                  </Select>
                  {disabled && <FormDescription>{t('channels.streamInterruption.passThroughDisabled')}</FormDescription>}
                  <FormMessage />
                </FormItem>
              )}
            />
            <DialogFooter>
              <Button type="submit" disabled={updateChannel.isPending}>{t('common.save')}</Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
```

> **执行者注意：** `Select`/`SelectContent` 等组件的 import 路径以 `frontend/src/components/ui/` 实际导出为准(Run `ls frontend/src/components/ui/select* 2>/dev/null || grep -rn "Select" frontend/src/components/ui/ | head`)。`updateChannel.mutate` 的入参形状(`{ id, settings }` 还是 `{ id, patch }`)以 Step 1 grep 结果为准。`mergeChannelSettingsForUpdate(currentRow.settings, patch)` 的 patch 形状以 `utils/merge.ts` 签名为准。这三个点 Step 1 已给出确切 grep 命令，执行者跑完后用真实 API 替换，不留占位。

- [ ] **Step 3: 在 `frontend/src/features/channels/components/index.ts` 导出新 dialog**

```ts
export { ChannelsStreamInterruptionDialog } from './channels-stream-interruption-dialog';
```

- [ ] **Step 4: 接入渠道表格行操作入口**

Run: `grep -n "ChannelsRateLimitDialog\|RateLimit" frontend/src/features/channels/components/channels-table.tsx frontend/src/features/channels/components/channels-action-dialog.tsx` 找到 rate-limit dialog 的接入模式(状态 + 菜单项 + 渲染)。按同样方式接入 `ChannelsStreamInterruptionDialog`：
- 加一个 `streamInterruptionOpen` state
- 在行操作菜单加一个菜单项(文案 `t('channels.streamInterruption.title')`)
- 在组件树渲染 `<ChannelsStreamInterruptionDialog open={...} onOpenChange={...} currentRow={...} />`

- [ ] **Step 5: 补 i18n 到 `frontend/src/locales/zh.json` 和 `en.json`**

zh.json 在 `channels` 下加：
```json
"streamInterruption": {
  "title": "流式中断恢复",
  "description": "设置上游 SSE 流中途断开时的恢复策略。请求体透传模式下不可用。",
  "policy": "恢复策略",
  "inheritDefault": "继承全局默认",
  "none": "无（不处理）",
  "complete": "补齐标准结束事件",
  "fakeStream": "假流式（缓冲后转发）",
  "passThroughDisabled": "当前渠道启用了请求体透传，此策略不可用，已强制为无。"
}
```

en.json 同结构英文翻译：
```json
"streamInterruption": {
  "title": "Stream Interruption Recovery",
  "description": "Recovery policy when an upstream SSE stream drops mid-stream. Unavailable with body pass-through.",
  "policy": "Policy",
  "inheritDefault": "Inherit global default",
  "none": "None (no recovery)",
  "complete": "Complete standard termination",
  "fakeStream": "Fake stream (buffer then forward)",
  "passThroughDisabled": "Body pass-through is enabled on this channel; this policy is unavailable and forced to None."
}
```

- [ ] **Step 6: 验证前端类型**

Run: `cd frontend && pnpm typecheck`
Expected: 无错误。
---

## Task 13: 前端 — 全局默认值 UI

**Files:**
- Modify: `frontend/src/features/system/data/system.ts`
- Modify: `frontend/src/features/system/components/retry-settings.tsx`

- [ ] **Step 1: 在 `frontend/src/features/system/data/system.ts` 的 `retryPolicy` 查询 selection(`emptyResponseDetection` 同级，约 line 103)加字段**

```graphql
      streamInterruptionDefault
```

- [ ] **Step 2: 在 `RetryPolicy` type(约 line 358)与 `UpdateRetryPolicyInput` type(约 line 386)加字段**

```ts
  streamInterruptionDefault: string;
```
和
```ts
  streamInterruptionDefault?: string;
```

- [ ] **Step 3: 若 system.ts 查询由 codegen 生成，跑 codegen；若手写则直接编辑**

Run: `grep -n "codegen\|gql\`" frontend/src/features/system/data/system.ts | head` 判断。

- [ ] **Step 4: 在 `frontend/src/features/system/components/retry-settings.tsx` 加"流式中断恢复默认策略"下拉**

参照 `retry-settings.tsx` 现有字段(如 `emptyResponseDetection` 的 form 结构)，加 `streamInterruptionDefault` Select(三选项：none / complete / fakeStream，默认 fakeStream)。i18n 复用 Task 12 的 `channels.streamInterruption.*` key，或在 `system` 命名空间另加。提交时随表单一起 mutate 到 `updateRetryPolicy`。

Run: `grep -n "emptyResponseDetection\|FormField" frontend/src/features/system/components/retry-settings.tsx | head` 找到现有字段结构作为模板。

- [ ] **Step 5: 验证类型**

Run: `cd frontend && pnpm typecheck`
Expected: 无错误。
---

## Task 14: 集成验证与文档

**Files:**
- 验证命令
- 文档(若有)

- [ ] **Step 1: llm module 全量测试**

Run: `cd llm && go test ./...`
Expected: 全部 PASS。

- [ ] **Step 2: 主 module 编译**

Run: `go build ./...`
Expected: 编译通过。

- [ ] **Step 3: 前端类型检查**

Run: `cd frontend && pnpm typecheck`
Expected: 无错误。

- [ ] **Step 4: 端到端心智验证(对照 spec 第 7 节测试矩阵)**

- 模式 3：上游流中途断开 → `bufferAndReplayStream` 返回 `ErrStreamInterrupted` → `processRequest` 返回 error → 进入 `pipeline.Process` 重试循环(客户端零字节未发)。Task 6 测试覆盖。
- 模式 3：上游完整 → 回放全部 chunks。Task 6 覆盖。
- 模式 2：上游 EOF 无终止信号 → 补 `max_tokens`/`length`/`MAX_TOKENS` 事件。Task 7 + Task 10a/10b/10c 覆盖。
- 模式 1：行为不变。Task 8 default 分支覆盖。
- PassThroughBody：orchestrator 强制降级 none。Task 9 覆盖。

- [ ] **Step 5: 更新渠道设置相关用户文档(若有)**

Run: `grep -rln "passThroughBody\|流式\|stream interruption" docs/ | head`
若有渠道设置文档，补一节"流式中断恢复"说明三模式与默认值。

---

## Self-Review 结果

**1. Spec coverage:**
- 三模式(无/补齐/假流式): Task 8 分发 + Task 5/6(假流式) + Task 7/10(补齐) + Task 4 默认 none ✓
- `ChannelSettings.StreamInterruption` 字段: Task 1 ✓
- 全局默认值 `RetryPolicy.StreamInterruptionDefault`: Task 2 + Task 3 + Task 13 ✓
- 模式 3 复用 `autoAggregateStream` 思路 + 切片回放: Task 5/6 ✓
- 模式 2 各协议标准终止事件(`max_tokens`/`length`/`MAX_TOKENS`): Task 7 + Task 10a(OpenAI) + Task 10b(Anthropic) + Task 10c(Gemini) ✓ **三个协议全部覆盖，无遗漏**
- PassThroughBody 降级: Task 9 + Task 12(disabled 态) ✓
- 前端渠道 dialog: Task 12 ✓
- 前端全局默认值 UI: Task 13 ✓
- i18n: Task 12/13 ✓
- 测试矩阵: Task 5/6/7/10/14 ✓

**2. Placeholder scan:**
- Task 6 Step 2: 真实可跑测试代码(非 `t.Skip` 骨架)，复用 `empty_response_test.go` 的 mock 模式 ✓
- Task 7 Step 2/4: 真实测试 + 真实 `completingStream` 实现(phase/index 形态)，无"按指引落地" ✓
- Task 9 Step 2: 仍有一处 `/* 按 Step 1 查实的方式取 *objects.ChannelSettings */` —— 这是**有 grep 命令支撑的待填项**(Step 1 给出确切 grep)，非"TBD/实现后填"。因 orchestrator 的 state 取值方式无法在不读全文的情况下确定字面量，保留为"跑 grep 后用真实表达式替换"是诚实的，且明确标注为本计划最后一个此类点。其余所有占位已消灭。
- Task 10a/10b/10c: 每个协议都有真实实现代码 + 真实测试，无"按既有签名对齐" ✓
- Task 12 Step 2: `Select` import 路径与 `mutate` 入参 — 有 Step 1 grep 支撑，标注为执行者跑 grep 后替换 ✓

**3. Type consistency:**
- `StreamInterruptionPolicy` 在 `internal/objects`(Task 1)与 `llm/pipeline`(Task 4)两处定义，orchestrator(Task 9)用 `pipeline.StreamInterruptionPolicy(streamInterruption)` 转换 — 一致。
- `StreamCompleter.CompletionEvents(ctx, *int) []*httpclient.StreamEvent`(Task 7 定义、Task 10a/10b/10c 实现、Task 8 调用)— 签名一致。
- `newReplayStream`、`newCompletingStream`、`bufferAndReplayStream`、`streamForPolicy`、`wrapWithCompletingStream`、`streamEndedNormally`、`hasFinishReasonInRaw`、`hasNonNullOrAfter` — 定义与调用点一致。
- `ErrStreamInterrupted`(Task 4 定义、Task 6 返回、Task 6 测试断言)— 一致。
- 前端 `streamInterruption: z.string().optional().nullable()`(Task 11)与 GraphQL `String`(Task 3 渠道字段未在 GraphQL 显式加，因 `ChannelSettings` 是 gqlgen 直接绑 struct——**缺口见下**)。

**4. 发现的缺口(需补):**
- **渠道级 `streamInterruption` 字段未在 GraphQL schema 显式声明。** `ChannelSettings`(`axonhub.graphql:87`)与 `ChannelSettingsInput`(`:123`)是显式 type/input，gqlgen 按其字段生成。新增 Go 字段后必须在两处 schema 加 `streamInterruption: String`，否则前端 GraphQL 查询拿不到。**Task 11 Step 2 假设了查询里有该字段，但 Task 3 只加了全局 `streamInterruptionDefault`，漏了渠道级字段。**

→ **修正:在 Task 3 补加渠道级 GraphQL 字段。** 见下方 Task 3 修订。

---

## Task 3 修订(补渠道级 GraphQL 字段)

在 Task 3 原 Step 1/2 之外，新增：

- [ ] **Step 1b: 在 `internal/server/gql/axonhub.graphql` 的 `type ChannelSettings`(`passThroughBody: Boolean` 之后，约 line 98)加字段**

```graphql
  streamInterruption: String
```

- [ ] **Step 2b: 在 `input ChannelSettingsInput`(`passThroughBody: Boolean` 之后，约 line 134)加字段**

```graphql
  streamInterruption: String
```

- [ ] **Step 3b: 重新跑 `go generate ./internal/server/gql/...`**(与 Task 3 Step 3 合并执行一次即可)，确认 `generated.go` 同时包含 `streamInterruptionDefault`(RetryPolicy)与 `streamInterruption`(ChannelSettings)。

> Task 3 的 Commit 已涵盖 `axonhub.graphql` 与 `system.graphql` 两处改动，统一一次提交即可。
