# 注册用户请求内容隔离 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 注册用户（非 owner）不能看到任何请求的请求体、响应体、响应分片、请求头；即便 owner 开启了请求存储也一样；前端查看这些内容的入口对注册用户不存在。

**Architecture:** 在两个层面拦截。① 数据加载层：`RequestService` 的 6 个 `Load*` 方法（请求体/响应体/分片，Request 与 RequestExecution 各 3 个）在开头判断当前用户是否 owner，非 owner 直接返回空。② 字段解析层：给 `Request.requestHeaders` 和 `RequestExecution.requestHeaders` 加 `forceResolver`，resolver 里同样按 owner 判断返回空（请求头含 `Authorization`/上游凭证，是最高危字段）。③ 访问控制层：给缺少 `Policy()` 的 `RequestExecution` 补上与 `Request` 一致的项目级 scope 策略。④ 前端：请求详情页对非 owner 隐藏 request/response/executions 的内容区块与所有 copy/download/curl 按钮。判断一律基于 `user.IsOwner`，不引入新 scope（个人项目，保持简单）。

**Tech Stack:** Go + Ent + gqlgen（后端），React + TanStack Query（前端）。代码生成用 `make generate`。

**权限事实（已核查）：**
- 注册用户系统级 scope 为空，仅个人项目内有 `read_requests` 等（`registration.go:99,235`）。
- `Request.Policy` 按 `project_id` + `read_requests` 隔离，跨项目隔离是严的；问题在于**项目内的请求内容**对注册用户也完全可见。
- `request_body/response_body/response_chunks` 已有 `forceResolver`，但 `Load*` 方法零权限校验（`request.go:1138-1345`）。
- `request_headers`（Request 与 RequestExecution）**无** `forceResolver`，作为普通字段随实体直接返回（`request.go:68-70`、`request_execution.go` 末尾）。
- `RequestExecution` **没有 `Policy()`**（`request_execution.go` 全文无 Policy 方法），通过 `request.executions` edge 可达，字段全部暴露。
- owner 判断现成模式：`contexts.GetUser(ctx)` + `user.IsOwner`（见 `backup.resolvers.go:56`、`system.resolvers.go:324`）。
- 前端请求详情组件 `request-detail-content.tsx` 无条件渲染所有内容区块；`usePermissions()` 已提供 `isOwner`。

**项目规则约束（来自 `.agent/rules/`）：**
- 改 Ent/GraphQL schema 后必须 `make generate`（`ent-graphql.md`）。
- biz 层用 `contexts.GetUser(ctx)` 读用户，不要读 ad hoc ctx 值（`biz-services.md`）。
- 在生成的 resolver 文件里只编辑生成的方法体（`ent-graphql.md`）。
- 不要手动写迁移 SQL，改 schema 后让 Ent 自管理（`ent-graphql.md`）。
- 不要运行 lint/build，除非用户明确要求（`AGENTS.md`）。

---

## File Structure

**后端（修改）：**
- `internal/server/biz/request.go` — 新增 `CanViewRequestContent(ctx)` helper；在 6 个 `Load*` 方法开头接入判断。
- `internal/server/biz/request_test.go` — 新增 helper 与 Load* 的权限测试。
- `internal/ent/schema/request.go` — 给 `request_headers` 字段加 `forceResolver`。
- `internal/ent/schema/request_execution.go` — 补 `Policy()` 方法；给 `request_headers` 字段加 `forceResolver`。
- `internal/server/gql/ent.resolvers.go` — 实现 `requestResolver.RequestHeaders` 与 `requestExecutionResolver.RequestHeaders`（仅编辑生成的方法体）。

**前端（修改）：**
- `frontend/src/features/requests/components/request-detail-content.tsx` — 按 `isOwner` 隐藏敏感内容区块与按钮。
- `frontend/src/locales/en/base.json`、`frontend/src/locales/zh-CN/base.json` — 新增"无权限查看"文案 key。

**代码生成产物（由 `make generate` 自动更新，不要手改）：**
- `internal/ent/*`、`internal/server/gql/generated.go`、`internal/server/gql/ent.graphql` 等。

---

### Task 1: 新增 `CanViewRequestContent` helper

**Files:**
- Modify: `internal/server/biz/request.go`（在 import 块后、`LoadRequestBody` 前）
- Test: `internal/server/biz/request_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/server/biz/request_test.go` 末尾追加（若文件不存在则创建，package `biz`）：

```go
package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
)

func TestCanViewRequestContent(t *testing.T) {
	// 无 user → false
	require.False(t, CanViewRequestContent(context.Background()))

	// 非 owner 注册用户 → false
	regularUser := &ent.User{IsOwner: false}
	require.False(t, CanViewRequestContent(contexts.WithUser(context.Background(), regularUser)))

	// owner → true
	owner := &ent.User{IsOwner: true}
	require.True(t, CanViewRequestContent(contexts.WithUser(context.Background(), owner)))
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/server/biz/ -run TestCanViewRequestContent -v`
Expected: FAIL，`undefined: CanViewRequestContent`

- [ ] **Step 3: 实现 helper**

在 `internal/server/biz/request.go` 的 import 块确认含 `"github.com/looplj/axonhub/internal/contexts"`（已有则跳过），在 `LoadRequestBody` 函数前插入：

```go
// CanViewRequestContent reports whether the current caller may read request
// bodies, responses, chunks, and headers. Only the system owner may view raw
// request content; registered (non-owner) users see metadata only.
func CanViewRequestContent(ctx context.Context) bool {
	user, ok := contexts.GetUser(ctx)
	return ok && user != nil && user.IsOwner
}
```

确认 `request.go` 顶部 import 含 `context`（已有）。若 `contexts` 未导入，添加 `"github.com/looplj/axonhub/internal/contexts"`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/server/biz/ -run TestCanViewRequestContent -v`
Expected: PASS

---

### Task 2: 在 6 个 `Load*` 方法接入 owner 判断

**Files:**
- Modify: `internal/server/biz/request.go:1138-1345`（`LoadRequestBody`、`LoadResponseBody`、`LoadResponseChunks`、`LoadRequestExecutionRequestBody`、`LoadRequestExecutionResponseBody`、`LoadRequestExecutionResponseChunks`）
- Test: `internal/server/biz/request_test.go`

每个方法在现有的 `if req == nil` / `if exec == nil` 守卫**之后**、任何读取之前，插入 owner 判断。返回值与方法签名一致。

- [ ] **Step 1: 写失败测试**

在 `request_test.go` 追加。该测试验证：非 owner 调用 `LoadRequestBody` 拿不到内容，owner 能拿到。

```go
func TestLoadRequestBody_OwnerGate(t *testing.T) {
	svc, client, ownerCtx, regularCtx, req := setupRequestContentTest(t)
	defer client.Close()

	// 非 owner → 空
	got, err := svc.LoadRequestBody(regularCtx, req)
	require.NoError(t, err)
	require.Nil(t, got)

	// owner → 真实内容（setupRequestContentTest 写入了固定 body）
	got, err = svc.LoadRequestBody(ownerCtx, req)
	require.NoError(t, err)
	require.Contains(t, string(got), `"model"`)
}
```

并新增共享 setup helper（放同一文件）。它初始化系统、建一个带 body 的 request，返回 owner 与非 owner 两个 ctx：

```go
func setupRequestContentTest(t *testing.T) (*RequestService, *ent.Client, context.Context, context.Context, *ent.Request) {
	t.Helper()

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	cacheConfig := xcache.Config{Mode: xcache.ModeMemory}
	systemService := NewSystemService(SystemServiceParams{CacheConfig: cacheConfig, Ent: client})
	svc := NewRequestService(RequestServiceParams{
		Ent:               client,
		SystemService:     systemService,
		DataStorageService: NewDataStorageService(DataStorageServiceParams{Ent: client}),
	})

	bypassCtx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	require.NoError(t, systemService.Initialize(bypassCtx, &InitializeSystemParams{
		OwnerEmail: "owner@example.com", OwnerPassword: "password123",
		OwnerFirstName: "Owner", OwnerLastName: "User", BrandName: "AxonHub",
	}))

	owner, err := client.User.Get(bypassCtx, 1)
	require.NoError(t, err)

	req, err := client.Request.Create().
		SetProjectID(1).
		SetModelID("gpt-4").
		SetFormat("openai/chat_completions").
		SetRequestBody(objects.JSONRawMessage(`{"model":"gpt-4","messages":[]}`)).
		Save(bypassCtx)
	require.NoError(t, err)

	ownerCtx := contexts.WithUser(ent.NewContext(context.Background(), client), owner)
	regularCtx := contexts.WithUser(ent.NewContext(context.Background(), client), &ent.User{IsOwner: false})
	return svc, client, ownerCtx, regularCtx, req
}
```

注意：`NewRequestService`/`NewDataStorageService` 的真实参数以代码中现有签名为准——执行此步前先读 `request.go` 顶部 `RequestServiceParams` 与 `NewRequestService`、`DataStorageServiceParams` 的定义，按实际字段填充；若 helper 名称/字段不符，以实际为准调整 setup（这是 setup 桩，不是被测逻辑）。

import 需补：`"github.com/looplj/axonhub/internal/objects"`、`"github.com/looplj/axonhub/internal/authz"`、`"github.com/looplj/axonhub/internal/ent/enttest"`、`"github.com/looplj/axonhub/internal/pkg/xcache"`、`"github.com/looplj/axonhub/internal/contexts"`（按需）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/server/biz/ -run TestLoadRequestBody_OwnerGate -v`
Expected: FAIL——非 owner 拿到了真实 body（断言 `got` 为 nil 失败）。

- [ ] **Step 3: 在 Load* 方法插入判断**

对每个 Load* 方法，在 nil 守卫之后插入：

```go
if !CanViewRequestContent(ctx) {
    return xjson.EmptyJSONRawMessage, nil
}
```

对两个返回 `[]objects.JSONRawMessage` 的方法（`LoadResponseChunks`、`LoadRequestExecutionResponseChunks`），改为：

```go
if !CanViewRequestContent(ctx) {
    return []objects.JSONRawMessage{}, nil
}
```

具体插入位置（都在 nil 守卫 `if req == nil`/`if exec == nil` 块之后的第一行）：
- `LoadRequestBody` — `request.go:1142` 之后
- `LoadResponseBody` — `request.go:1176` 之后
- `LoadResponseChunks` — `request.go:1215` 之后（注意此方法在 nil 守卫后还有 live-stream 分支 `req.Stream && req.Status == Processing`；owner 判断要放在 live-stream 分支**之前**，确保非 owner 连实时预览也看不到）
- `LoadRequestExecutionRequestBody` — `request.go:1261` 之后
- `LoadRequestExecutionResponseBody` — `request.go:1295` 之后
- `LoadRequestExecutionResponseChunks` — `request.go:1334` 之后（同样在 live-stream 分支之前）

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/server/biz/ -run TestLoadRequestBody_OwnerGate -v`
Expected: PASS

- [ ] **Step 5: 补一个对称测试覆盖 ResponseChunks（空切片返回）**

```go
func TestLoadResponseChunks_OwnerGateReturnsEmptySlice(t *testing.T) {
	svc, client, _, regularCtx, req := setupRequestContentTest(t)
	defer client.Close()

	got, err := svc.LoadResponseChunks(regularCtx, req)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
}
```

Run: `go test ./internal/server/biz/ -run TestLoadResponseChunks_OwnerGateReturnsEmptySlice -v`
Expected: PASS

---

### Task 3: 给 `Request.requestHeaders` 加 forceResolver 并生成代码

**Files:**
- Modify: `internal/ent/schema/request.go:68-70`

- [ ] **Step 1: 改 schema**

`internal/ent/schema/request.go:68-70` 当前：

```go
		field.JSON("request_headers", objects.JSONRawMessage{}).
			Optional().
			Comment("Request headers"),
```

改为：

```go
		field.JSON("request_headers", objects.JSONRawMessage{}).
			Optional().
			Comment("Request headers").
			Annotations(
				entgql.Directives(forceResolver()),
			),
```

- [ ] **Step 2: 生成代码**

Run: `make generate`
Expected: 成功；`internal/server/gql/ent.resolvers.go` 出现 `func (r *requestResolver) RequestHeaders(...)` 方法桩。

- [ ] **Step 3: 确认 resolver 桩已生成**

Run: `grep -n "requestResolver) RequestHeaders" internal/server/gql/ent.resolvers.go`
Expected: 输出对应行号（Task 4 在此实现）。

---

### Task 4: 实现 `Request.requestHeaders` resolver（owner 判断）

**Files:**
- Modify: `internal/server/gql/ent.resolvers.go`（生成的 `requestResolver.RequestHeaders` 方法体）

- [ ] **Step 1: 实现方法体**

找到 Task 3 生成的 `func (r *requestResolver) RequestHeaders(ctx context.Context, obj *ent.Request) (objects.JSONRawMessage, error)`，把方法体替换为：

```go
func (r *requestResolver) RequestHeaders(ctx context.Context, obj *ent.Request) (objects.JSONRawMessage, error) {
	if !biz.CanViewRequestContent(ctx) {
		return xjson.EmptyJSONRawMessage, nil
	}

	if obj.RequestHeaders == nil {
		return xjson.EmptyJSONRawMessage, nil
	}

	return obj.RequestHeaders, nil
}
```

确认 `ent.resolvers.go` import 含 `"github.com/looplj/axonhub/internal/server/biz"` 与 `"github.com/looplj/axonhub/internal/pkg/xjson"`（其他 resolver 已用 `biz`/`xjson`，通常已导入；缺则补）。

- [ ] **Step 2: 确认编译**

Run: `go build ./internal/server/gql/`
Expected: 无报错。

---

### Task 5: 给 `RequestExecution` 补 Policy 并给其 requestHeaders 加 forceResolver

**Files:**
- Modify: `internal/ent/schema/request_execution.go`

- [ ] **Step 1: 给 requestHeaders 加 forceResolver**

`request_execution.go` 末尾的 `request_headers` 字段当前：

```go
		field.JSON("request_headers", objects.JSONRawMessage{}).
			Optional().
			Comment("Request headers"),
```

改为：

```go
		field.JSON("request_headers", objects.JSONRawMessage{}).
			Optional().
			Comment("Request headers").
			Annotations(
				entgql.Directives(forceResolver()),
			),
```

- [ ] **Step 2: 补 Policy 方法**

在 `request_execution.go` 的 `Annotations()` 方法后追加（与 `Request.Policy` 一致，按 `read_requests`/`write_requests` 项目级隔离）。先确认文件 import 含 `"github.com/looplj/axonhub/internal/scopes"`，若无则添加：

```go
func (RequestExecution) Policy() ent.Policy {
	return scopes.Policy{
		Query: scopes.QueryPolicy{
			scopes.APIKeyScopeQueryRule(scopes.ScopeWriteRequests),
			scopes.UserProjectScopeReadRule(scopes.ScopeReadRequests),
			scopes.OwnerRule(),
			scopes.UserReadScopeRule(scopes.ScopeReadRequests),
		},
		Mutation: scopes.MutationPolicy{
			scopes.APIKeyScopeMutationRule(scopes.ScopeWriteRequests),
			scopes.UserProjectScopeWriteRule(scopes.ScopeWriteRequests),
			scopes.OwnerRule(),
			scopes.UserWriteScopeRule(scopes.ScopeWriteRequests),
		},
	}
}
```

- [ ] **Step 3: 生成代码**

Run: `make generate`
Expected: 成功；生成 `requestExecutionResolver.RequestHeaders` 方法桩。

- [ ] **Step 4: 确认 resolver 桩**

Run: `grep -n "requestExecutionResolver) RequestHeaders" internal/server/gql/ent.resolvers.go`
Expected: 输出行号。

---

### Task 6: 实现 `RequestExecution.requestHeaders` resolver

**Files:**
- Modify: `internal/server/gql/ent.resolvers.go`（生成的 `requestExecutionResolver.RequestHeaders` 方法体）

- [ ] **Step 1: 实现方法体**

```go
func (r *requestExecutionResolver) RequestHeaders(ctx context.Context, obj *ent.RequestExecution) (objects.JSONRawMessage, error) {
	if !biz.CanViewRequestContent(ctx) {
		return xjson.EmptyJSONRawMessage, nil
	}

	if obj.RequestHeaders == nil {
		return xjson.EmptyJSONRawMessage, nil
	}

	return obj.RequestHeaders, nil
}
```

- [ ] **Step 2: 确认编译**

Run: `go build ./internal/server/gql/`
Expected: 无报错。

---

### Task 7: 前端请求详情页按 `isOwner` 隐藏敏感内容

**Files:**
- Modify: `frontend/src/features/requests/components/request-detail-content.tsx`
- Modify: `frontend/src/locales/en/base.json`、`frontend/src/locales/zh-CN/base.json`

非 owner 在请求详情页只能看到：概览卡片（状态/模型/apiKey）、usage token 统计。Request/Response/Executions 三个 tab 内的请求体/响应体/分片/请求头区块及所有 Copy/Download/Curl 按钮一律隐藏，改显示"无权限查看请求内容"提示。

- [ ] **Step 1: 加 i18n 文案**

在 `frontend/src/locales/en/base.json` 的 `requests.detail` 对象内加：

```json
"noPermissionContent": "You do not have permission to view request content."
```

在 `frontend/src/locales/zh-CN/base.json` 对应位置加：

```json
"noPermissionContent": "您没有权限查看请求内容。"
```

（先读这两个文件确认 `requests.detail` 的实际嵌套结构与逗号位置，按现有风格插入，避免 JSON 语法错误。）

- [ ] **Step 2: 取 isOwner 并定义无权限提示块**

在 `request-detail-content.tsx` 顶部 `usePermissions()` 解构处（当前第 49 行 `const { hasSystemScope } = usePermissions();`）改为：

```tsx
const { hasSystemScope, isOwner } = usePermissions();
```

在组件内 `const hasResponseBody = ...` 一类常量附近，加一个复用提示组件（在组件函数体内、return 之前）：

```tsx
const NoPermissionNotice = () => (
  <div className='bg-muted/20 flex h-[300px] w-full items-center justify-center rounded-lg border p-6'>
    <div className='space-y-3 text-center'>
      <FileText className='text-muted-foreground mx-auto h-12 w-12' />
      <p className='text-muted-foreground text-base'>{t('requests.detail.noPermissionContent')}</p>
    </div>
  </div>
);
```

- [ ] **Step 3: request tab —— 非 owner 隐藏 headers + body + curl 按钮**

把当前 `<TabsContent value='request' ...>`（约 505-561 行）的内容替换为按 `isOwner` 分支：

```tsx
<TabsContent value='request' className='space-y-6 p-6'>
  {!isOwner ? (
    <NoPermissionNotice />
  ) : (
    <>
      <div className='flex justify-end'>
        <Button
          variant='outline'
          size='sm'
          onClick={() => showRequestCurlPreview(request.requestHeaders, request.requestBody, request.format)}
          className='hover:bg-primary hover:text-primary-foreground'
        >
          <Terminal className='mr-2 h-4 w-4' />
          {t('requests.actions.copyCurl')}
        </Button>
      </div>
      {request.requestHeaders && (
        <div className='space-y-4'>
          <div className='flex items-center justify-between'>
            <h4 className='flex items-center gap-2 text-base font-semibold'>
              <FileText className='text-primary h-4 w-4' />
              {t('requests.columns.requestHeaders')}
            </h4>
            <div className='flex gap-2'>
              <Button variant='outline' size='sm' onClick={() => copyToClipboard(formatJson(request.requestHeaders))} className='hover:bg-primary hover:text-primary-foreground'>
                <Copy className='mr-2 h-4 w-4' />
                {t('requests.dialogs.jsonViewer.copy')}
              </Button>
              <Button variant='outline' size='sm' onClick={() => downloadFile(formatJson(request.requestHeaders), `request-headers-${request.id}.json`)} className='hover:bg-primary hover:text-primary-foreground'>
                <Download className='mr-2 h-4 w-4' />
                {t('requests.dialogs.jsonViewer.download')}
              </Button>
            </div>
          </div>
          <div className='bg-muted/20 h-[300px] w-full overflow-auto rounded-lg border p-4'>
            <JsonViewer data={request.requestHeaders} rootName='' defaultExpanded={true} expandDepth='all' hideArrayIndices={true} className='text-sm' />
          </div>
        </div>
      )}
      <div className='space-y-4'>
        <div className='flex items-center justify-between'>
          <h4 className='flex items-center gap-2 text-base font-semibold'>
            <FileText className='text-primary h-4 w-4' />
            {t('requests.columns.requestBody')}
          </h4>
          <div className='flex gap-2'>
            <Button variant='outline' size='sm' onClick={() => copyToClipboard(formatJson(request.requestBody))} className='hover:bg-primary hover:text-primary-foreground'>
              <Copy className='mr-2 h-4 w-4' />
              {t('requests.dialogs.jsonViewer.copy')}
            </Button>
            <Button variant='outline' size='sm' onClick={() => downloadFile(formatJson(request.requestBody), `request-body-${request.id}.json`)} className='hover:bg-primary hover:text-primary-foreground'>
              <Download className='mr-2 h-4 w-4' />
              {t('requests.dialogs.jsonViewer.download')}
            </Button>
          </div>
        </div>
        <div className='bg-muted/20 h-[500px] w-full overflow-auto rounded-lg border p-4'>
          <JsonViewer data={request.requestBody} rootName='' defaultExpanded={true} expandDepth='all' hideArrayIndices={true} className='text-sm' />
        </div>
      </div>
    </>
  )}
</TabsContent>
```

- [ ] **Step 4: response tab —— 非 owner 整块隐藏**

把 `<TabsContent value='response' ...>`（约 563-710 行）最外层用 `isOwner` 包裹。在进入该 TabsContent 后立即判断：

```tsx
<TabsContent value='response' className='space-y-6 p-6'>
  {!isOwner ? (
    <NoPermissionNotice />
  ) : (
    <Tabs value={responseView} onValueChange={(v: any) => setResponseView(v)} className='w-full'>
      {/* ...existing response tab children unchanged... */}
    </Tabs>
  )}
</TabsContent>
```

即把现有 `<Tabs value={responseView} ...>...</Tabs>` 整体作为 `isOwner` 为真时的分支，原样保留其内部所有按钮与 preview/json 内容；非 owner 渲染 `NoPermissionNotice`。

- [ ] **Step 5: executions tab —— 非 owner 隐藏每条 execution 的 headers/body/response/curl**

在 executions tab 渲染每个 `execution` 的 `<CardContent>` 内（约 746-896 行），用 `isOwner` 包裹「curl 按钮 + requestHeaders + requestBody + responseBody」这四个区块，非 owner 显示提示：

```tsx
{!isOwner ? (
  <NoPermissionNotice />
) : (
  <>
    {(execution.requestHeaders || execution.requestBody) && (
      <div className='flex justify-end'>
        <Button variant='outline' size='sm' onClick={() => showExecutionCurlPreview(execution.requestHeaders, execution.requestBody, execution.channel, execution.format)} className='hover:bg-primary hover:text-primary-foreground'>
          <Terminal className='mr-2 h-4 w-4' />
          {t('requests.actions.copyCurl')}
        </Button>
      </div>
    )}
    {execution.requestHeaders && (
      /* ...existing requestHeaders block unchanged... */
    )}
    {execution.requestBody && (
      /* ...existing requestBody block unchanged... */
    )}
    {execution.responseBody && (
      /* ...existing responseBody block unchanged... */
    )}
  </>
)}
```

保留 execution 概览（channel/start/end/latency/status/error）在 `isOwner` 判断**之外**，因为那是元数据。把现有的四个内容区块整体放进 `isOwner ? (...) : <NoPermissionNotice />`。

- [ ] **Step 6: 手动验证（dev server 已在运行，不要重启）**

1. 以 owner 登录，打开任一请求详情：request/response/executions 内容正常可见。
2. 以注册用户（非 owner）登录，打开 `/project/requests`，点 View Details：三个 tab 显示"无权限查看请求内容"，无 curl/copy/download 按钮。
3. 用注册用户 token 直接发 GraphQL 查询请求的 `requestBody`/`responseBody`/`responseChunks`/`requestHeaders` 及 execution 的同类字段：全部返回空。

---

### Task 8: 全量回归验证

- [ ] **Step 1: 后端测试**

Run: `go test ./internal/server/biz/ -run "Request|CanView" -v`
Expected: 全部 PASS。

- [ ] **Step 2: 前端类型检查（仅类型，不跑完整 build）**

Run: `cd frontend && npx tsc --noEmit`
Expected: 无类型错误。

---

## Self-Review 结论

- **Spec 覆盖**：用户需求2 = "注册用户不应看到请求体、响应内容，查看入口不该存在"。Task 2（body/chunks）、Task 4（Request headers）、Task 6（Execution headers）、Task 7（前端入口隐藏）完整覆盖；Task 5 的 Policy 补丁堵住 RequestExecution 这条独立泄露路径。`clientIP`/`cost` 不在本需求范围（用户只点名请求体/响应内容），未纳入。
- **占位符扫描**：无 TBD/TODO；setup helper 的 service 构造已注明"按实际签名核对"。
- **类型一致性**：`CanViewRequestContent` 在 biz 定义、在 4 处（6 个 Load* + 2 个 resolver）调用，签名一致。
