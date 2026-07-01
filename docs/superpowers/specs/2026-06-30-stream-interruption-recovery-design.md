# 渠道级 SSE 流中断恢复策略 — 设计文档

- 日期: 2026-06-30
- 状态: 已批准（待实现）
- 范围: `internal/objects/channel.go`、`llm/pipeline/`、`internal/server/biz/system.go`、前端渠道设置

## 1. 背景与问题

axonhub 作为 OpenAI/Anthropic/Gemini 兼容的 API 网关，向客户端转发上游 provider 的 SSE 流。当前存在一个未覆盖的故障窗口：

**首事件之后**的上游流中途断开（连接被切断、provider 超时、反向代理中断等）**完全不可恢复**。原因是 `pipeline.Process` 的重试循环只覆盖到"首事件之前"（建连失败、首事件超时 `ErrStreamFirstEventTimeout`、空响应 `ErrEmptyResponse`，均在 `pipe.Process` 返回前、HTTP 200 + SSE 头未发出）。一旦首事件通过 `preReadLlmStream` 校验、`pipe.Process` 返回 `EventStream`，重试循环已退出。

更糟的是 `llm/httpclient/decoder.go:139` 把上游的 `io.EOF` 一律当作**正常结束**，`Err()` 为 nil——既不重试，也不补发该协议本该有的标准终止信号。客户端因此收到一个"半截、无终止语义"的流：

- **claude-code**（`src/services/api/claude.ts:2350`）判定流"不完整需报错"的条件为 `!partialMessage || (newMessages.length === 0 && !stopReason)`，触发回退到非流式重试，**可能导致工具被重复执行**。
- **omp**（oh-my-pi）较宽容但仍可能 warn/报错。

### 客户端兼容性约束（硬约束）

> 严格按各 provider 协议（OpenAI / Anthropic / Gemini）的标准 SSE 语义处理，**axonhub 不得自创或扩展 SSE 协议结构**。客户端用官方 SDK 即可正常解析。

经研究 claude-code 与 omp 两个真实客户端，结论一致：客户端对"流中途断开"本身宽容，只要收到该协议**标准终止信号**（Anthropic 的 `message_delta`(带 `stop_reason`) + `message_stop`；OpenAI 的带 `finish_reason` 的 chunk + `[DONE]`）即可走正常的成功收尾路径，不报错、不重试、不回退。

## 2. 三种恢复模式

在渠道设置中新增"流式中断恢复策略"，三选一：

| 模式 | 名称 | 行为 | 优点 | 缺点 |
|---|---|---|---|---|
| 1 | 无 (`none`) | 不处理（现状）。上游断了，客户端收到截断流。 | 不掩盖上游真实故障，排障直接。 | 客户端可能报错/回退。 |
| 2 | 补齐标准结束 (`complete`) | 上游断了，按当前协议补发**标准**终止事件（`max_tokens`/`length` 语义），客户端正常结束。 | 实现简单，客户端不报错。 | **破坏模型行为语义**：伪造了"被截断"为"达到上限"，掩盖上游故障；客户端基于假信号决策。 |
| 3 | 假流式 (`fakeStream`) | 服务端**全量缓冲**上游流，完整后再以 SSE 回放给客户端；缓冲阶段上游断了走现有重试，客户端零字节未发、完全无感。 | 不破坏模型行为、客户端无感、复用现有重试。 | 丧失真流式实时性：首 token 延迟 = 上游完整生成时间；服务端需缓冲整个响应。 |

### 默认值

**模式 3（假流式）**。理由：唯一不破坏模型行为、客户端完全无感、复用现有重试机制的模式。渠道可在设置里改为 1 或 2。

### 模式 2 的 stop_reason 语义

按语义：上游断了 = 模型未说完即被截断 = `max_tokens`（Anthropic）/ `length`（OpenAI）。这是唯一诚实的映射：

- `end_turn`/`stop` 表示"模型主动说完了"——为假，会误导客户端。
- `max_tokens`/`length` 表示"因上限截断"——语义最贴近"中途断了"。claude-code 对 `max_tokens` 会给用户"内容可能被截断"的提示，正符合预期，且不触发非流式回退。

## 3. 数据结构

在 `internal/objects/channel.go` 的 `ChannelSettings` 中新增字段，沿用 `PassThroughBody` 的 `*T` 指针风格（`nil` = 继承全局默认；非 `nil` = 覆盖）：

```go
// StreamInterruptionPolicy controls how the channel handles upstream SSE
// streams that end abnormally (connection drop mid-stream without the
// protocol's standard termination signal).
type StreamInterruptionPolicy string

const (
    StreamInterruptionNone       StreamInterruptionPolicy = "none"
    StreamInterruptionComplete   StreamInterruptionPolicy = "complete"
    StreamInterruptionFakeStream StreamInterruptionPolicy = "fakeStream"
)

// StreamInterruption controls the upstream stream-interruption recovery policy.
// nil = inherit global default (fakeStream); otherwise overrides per channel.
// Not effective when PassThroughBody is enabled (fake-stream buffering and
// complete-event synthesis require the transform pipeline).
StreamInterruption *StreamInterruptionPolicy `json:"streamInterruption,omitempty"`
```

全局默认值放在 system settings 中（与 `RetryPolicy` 同级，`internal/server/biz/system.go`），默认 `fakeStream`，供渠道级 `nil` 继承。`system_default.go` 同步更新默认值。

由于 `ChannelSettings` 持久化在 Ent 的 JSON 字段中，新增可选字段不破坏既有数据兼容性（遵循 `.agent/rules/cache-compat.md`）。

## 4. 实现落点

### 模式 1（无）

`processRequest`（`llm/pipeline/pipeline.go:372`）按 `originalWantStream` 走现有 `p.stream(...)`，行为不变。

### 模式 3（假流式）— 核心

新增 `bufferAndReplayStream`，复用 `autoAggregateStream`（`llm/pipeline/non_streaming.go:81`）的"向上游发流式 + 全量缓冲"逻辑，但最后一步不是聚合成非流式 JSON，而是**把缓冲的 chunks 原样回放为 SSE 流**：

1. `inboundStream, err := p.stream(ctx, executor, httpReq, 0)`（同 `non_streaming.go:86`，向上游发流式，`firstEventTimeout=0` 表示缓冲阶段不启用首事件超时——总时长由请求级 context 边界约束）。`EmptyResponseDetection` 在缓冲阶段照常生效（空响应命中 `ErrEmptyResponse` 正好该重试）。
2. 全量缓冲：`for inboundStream.Next() { chunks = append(chunks, inboundStream.Current()) }`（同 `non_streaming.go:92-98`）。
3. **完整性校验**：
   - 若 `inboundStream.Err() != nil` → 返回该 error。
   - 若缓冲为空 → 返回 `ErrEmptyStreamChunks`。
   - 若最后一个有意义事件不是该协议的标准终止信号（统一锚点：`llm.DoneResponse`，即 `Object == "[DONE]"`；或 `Response.Choices[].FinishReason != nil`，复用 `hasFinishReason` `stream.go:135`）→ 返回新错误 `ErrStreamInterrupted`（表示"流未正常结束"）。
4. 完整 → 把 `chunks` 包装成一个切片回放流 `replayStream`（实现 `streams.Stream[*httpclient.StreamEvent]` 接口：`Next`/`Current`/`Err`/`Close`，按序 yield 缓冲事件后返回 false）。设 `result.Stream = true`、`result.EventStream = replayStream`。

**关键**：步骤 3 的所有错误路径都让 `bufferAndReplayStream` 返回 error，`processRequest` 因此返回 error → 进入 `pipeline.Process` 的现有重试循环（`pipeline.go:286-369`，`ChannelRetryable` 同渠道重试 + `Retryable` 切渠道）。此时 HTTP 200 + SSE 头**尚未发出**（客户端零字节未发），重试完全透明。这正是"报错时自动就重试了"的来源。

**`ErrStreamInterrupted` 的重试归类**：归为可重试错误（同 `ErrEmptyResponse`、`ErrStreamFirstEventTimeout` 一类，不进 `RetrySkipper` 的不可重试集合），确保触发重试而非直接返回。

### `processRequest` 分支调整

在现有 `switch`（`pipeline.go:403`）前，先解析 effective 策略（合并全局默认 + 渠道覆盖）。分支逻辑：

- `originalWantStream && effectivePolicy == fakeStream` → `bufferAndReplayStream`，设 `result.Stream = true`。
- `originalWantStream && effectivePolicy == none` → 现有 `p.stream(...)`，不变。
- `originalWantStream && effectivePolicy == complete` → 现有 `p.stream(...)`，但其 `inboundStream` 链路包一层"完整性检测 + 补齐"（见模式 2）。
- `originalWantStream == false` 的各分支不变。

effective 策略的解析需要渠道信息。pipeline 当前通过 `ChannelCustomizedExecutor` 等 hook 感知渠道；策略字段经 orchestrator（`internal/server/orchestrator/orchestrator.go:210-340`）装配时注入 pipeline（新增一个 pipeline option，如 `WithStreamInterruptionPolicy(policy)`）。

### 模式 2（补齐标准结束）

在 inbound 流链路增加一个"完整性检测 + 补齐"的流包装 `completingStream`，包裹在 `cancelOnCloseStream`（`stream.go:407`）之前。逻辑：

- 包裹底层 `inboundStream`，透传所有 `Next`/`Current`。
- 当底层 `Next()` 返回 false（流结束）时：
  - 若 `Err() != nil`（非 EOF 的传输错误）→ 透传错误（仍会写 `error` SSE 事件，但这是已有行为，模式 2 不改变）。
  - 若 `Err() == nil` 但未见过标准终止信号（同模式 3 的判定锚点）→ 按 inbound 协议补发标准终止事件：
    - **Anthropic**：若有未关闭的 content block → 补 `content_block_stop`；补 `message_delta`（`delta.stop_reason: "max_tokens"` + `usage`，`usage.output_tokens` 用已累积事件的估算值，无法精确时填 0 或省略，不阻塞收尾）；补 `message_stop`。
    - **OpenAI**：补一个带 `finish_reason: "length"` 的 chunk；补 `data: [DONE]`。
    - **Gemini**：按其标准终止事件补齐。
  - 补发后 `Next` 返回 false，`Err()` 为 nil。

模式 2 是流式直传（非缓冲），无法拿到精确 usage；`usage` 字段尽力从已收事件累积估算，无法估算时省略——客户端对 `max_tokens` 主要看 `stop_reason` 语义，usage 不影响收尾判定。

补发逻辑由 inbound transformer 提供（每个协议各自实现，不发明新协议）。pipeline 通过新接口（如 `Inbound.StreamCompletionEvents(ctx, interrupted bool) []*httpclient.StreamEvent`）获取要补的事件，避免 pipeline 硬编码协议细节。

## 5. 作用范围与边界

- 仅对**流式请求**生效（`originalWantStream == true`）。
- **PassThroughBody 渠道**：模式 2、3 均不适用（假流式缓冲与补齐事件合成都依赖 transform 管道）。当 `PassThroughBody` 启用时，策略强制降级为 `none`，并在日志中记录降级。
- 三模式均**不改变**发给客户端的 SSE 事件格式，严格符合各协议标准。
- 模式 3 的缓冲阶段不应用 `StreamFirstEventTimeout`（该超时面向"首事件 flush 给客户端"语义），改由请求级 context 边界约束总时长。
- 模式 3 缓冲整个响应到内存：对超长回复需注意内存开销，但这是模式 3 的固有代价，文档与 UI 应提示。

## 6. 前端

渠道设置页新增"流式中断恢复"下拉（无 / 补齐标准结束 / 假流式），默认假流式。复用现有渠道设置组件结构（`frontend/src/features/channels/components/`）。i18n 补 `frontend/src/locales/zh.json`、`en.json`。当渠道启用 PassThroughBody 时，下拉禁用并提示"透传模式下不可用"。

## 7. 测试

- **模式 3**：
  - 模拟上游流中途断开 → 断言 `bufferAndReplayStream` 返回 `ErrStreamInterrupted`、`processRequest` 返回 error、客户端零字节未发、重试被触发。
  - 模拟上游完整 → 断言客户端收到完整 SSE 回放（与原流事件序列一致）。
  - 模拟上游空响应 → 断言 `ErrEmptyStreamChunks` 并触发重试。
- **模式 2**：
  - 模拟上游 EOF 缺终止信号 → 断言补发了 `max_tokens`（Anthropic）/ `length`（OpenAI）终止事件。
  - 模拟上游正常结束 → 断言不重复补发。
- **模式 1**：断言行为不变（截断流，无补发，无重试）。
- PassThroughBody 启用时断言降级为 `none`。

## 8. 关键文件

- `internal/objects/channel.go` — `ChannelSettings.StreamInterruption` 字段与枚举。
- `llm/pipeline/pipeline.go` — `processRequest` 分支调整、策略解析。
- `llm/pipeline/non_streaming.go` — 新增 `bufferAndReplayStream`。
- `llm/pipeline/stream.go` — `completingStream` 包装、`ErrStreamInterrupted`。
- `llm/transformer/interfaces.go` — inbound 补齐事件接口。
- `internal/server/biz/system.go` / `system_default.go` — 全局默认值。
- `internal/server/orchestrator/orchestrator.go` — 策略装配到 pipeline。
- `frontend/src/features/channels/components/` — 渠道设置 UI。
- `frontend/src/locales/{zh,en}.json` — i18n。
