# 注册用户体验增强 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让注册用户（非 owner）登录后有可用、可观测的体验——不再误入 dashboard 错误页、能查自己 API Key 的用量、有「我的用量」可视化看板、能看到模型可用性（但看不到渠道配置）。

**Architecture:** 四个独立增益，按价值/成本排序。① 前端 `/` 路由加 `beforeLoad`，无 `read_dashboard` 的用户 redirect 到 playground，避免错误页。② 后端 `apiKeyTokenUsageStats` 把 `RequireScope(read_api_keys)`（不认项目级 scope）改为注入 projectID + `WithScopeDecision(read_api_keys)`，让注册用户能查自己项目内 key 的用量。③ 新增按当前项目过滤的用量聚合 query（复用 ent privacy 的项目级 scope 放行）+ 前端 `/project/usage` 看板页。④ 新增 `modelAvailability` 脱敏 query（owner/注册用户都可用，systemBypass 聚合，只返回模型维度的可用性/能力，绝不挂渠道身份）+ 前端模型可用性面板。

**Tech Stack:** Go + Ent + gqlgen（后端），React + TanStack Router/Query + recharts（前端）。代码生成 `make generate`。

**已核查事实：**
- 注册用户系统级 scope 为空，个人项目内有 `read_api_keys/read_requests/read_prompts`（`registration.go:99,235`）。
- dashboard 所有 query 用 `WithScopeDecision(read_dashboard)`，注册用户无 `read_dashboard` 且 dashboard hook 不传 `X-Project-ID` → 整条 privacy deny，访问 `/` 显示 loadError（`dashboard/index.tsx:134-142`，`dashboard.resolvers.go` 多处）。
- `RequireScope` 走 `userHasScope`，**只认 `user.Scopes` + 系统级 role，不认项目级 `UserProject.Scopes`**（`authz/scope.go:68-93`）；`WithScopeDecision` + 注入 projectID 则能让 `UserProjectScopeReadRule` 生效（`rule_user_project_scope.go`）。
- `/` 路由无 `beforeLoad`/`RouteGuard`（`routes/_authenticated/index.tsx`）；非 owner 登录跳 `/project/playground`（`auth.ts:86`）。
- `apiKeyTokenUsageStats` 在 `dashboard.resolvers.go:424` 用 `RequireScope(read_api_keys)`，前端 hook 传 `X-Project-ID`（`features/apikeys/data/apikeys.ts:493+`）。
- ent privacy 规则：`UserProjectScopeReadRule(scope)` 检查 ctx 中的 projectID 与 user 在该 project 的 scope。
- 模型/可用性数据源已存在：`ListEnabledModels`（`biz/model.go`，返回 `ModelFacade{ID,DisplayName,OwnedBy}`）、`ModelCard`（`objects/model.go:26`，含 vision/toolCall/streaming/reasoning）、`channelSuccessRates`/`fastestModels`（`dashboard.resolvers.go`，按 model 聚合）。
- recharts 已是依赖；dashboard 组件可复用。

**项目规则约束：** 改 GraphQL schema 后 `make generate`（`ent-graphql.md`）；biz 用 `contexts.GetUser(ctx)`；prefer GraphQL-side filtering（`ent-graphql.md`）；改字段时同步全链路（schema→generate→前端 operations）；不跑 lint/build 除非明确要求；不重启 dev server。

**与计划1的关系：** 本计划独立，可先于/后于「请求内容隔离」计划实施。两者无文件冲突（计划1改请求详情，本计划改 dashboard/usage/playground）。

---

## File Structure

**后端（修改/新增）：**
- `internal/server/gql/dashboard.resolvers.go` — 修 `APIKeyTokenUsageStats` 权限；新增 `ProjectUsageOverview`、`ProjectDailyRequestStats`、`ModelAvailability` resolver。
- `internal/server/gql/dashboard.graphql` — 新增上述 query/type 定义。
- `internal/server/biz/model.go`（或新文件 `internal/server/biz/model_availability.go`）— 新增 `ListModelAvailability(ctx)` 聚合方法。
- `internal/server/gql/dashboard.resolvers_test.go`（若不存在则新建）— 权限回归测试。

**前端（修改/新增）：**
- `frontend/src/routes/_authenticated/index.tsx` — 加 `beforeLoad` redirect。
- `frontend/src/routes/_authenticated/project/usage.tsx` — 新建个人用量看板页路由。
- `frontend/src/features/project-usage/` — 新建看板 feature（data hook + 组件）。
- `frontend/src/features/playground/components/model-availability-panel.tsx`（或 sidebar 入口）— 模型可用性面板。
- `frontend/src/gql/` — 新增/重新生成 GraphQL operations。
- `frontend/src/sidebar.ts` — Project 组加「用量」菜单项（若需）。
- `frontend/src/locales/{en,zh-CN}/base.json` — 新增文案。

---

### Task 1: `/` 路由 redirect 非 dashboard 用户

**Files:**
- Modify: `frontend/src/routes/_authenticated/index.tsx`

- [ ] **Step 1: 读现有路由文件**

Run: 读 `frontend/src/routes/_authenticated/index.tsx`，确认其 `createFileRoute('/_authenticated/')` 结构与 import（需 `redirect` from `@tanstack/react-router`、`usePermissions` 或直接读 authStore 的 isOwner）。

- [ ] **Step 2: 加 beforeLoad**

在 `createFileRoute` 的选项对象里加 `beforeLoad`，无 `read_dashboard` 能力（注册用户）重定向到 `/project/playground`：

```tsx
export const Route = createFileRoute('/_authenticated/')({
  beforeLoad: ({ context, location }) => {
    // context.auth 或直接从 store 取 isOwner / scopes
    const isOwner = context.auth?.user?.isOwner ?? false;
    const hasDashboard = isOwner || (context.auth?.user?.scopes ?? []).includes('read_dashboard');
    if (!hasDashboard) {
      throw redirect({ to: '/project/playground', replace: true });
    }
  },
  component: Dashboard,
});
```

注意：`context.auth` 的实际形状以 router 根 context（`__root.tsx` / `router.ts`）为准——先读 `frontend/src/routes/__root.tsx` 与创建 router 的文件，确认如何取当前 user/isOwner。若根 context 未暴露 auth，改为在 `beforeLoad` 里从 `authStore.getState()` 同步取（`authStore` 是 zustand，支持非 hook 取值）：

```tsx
import { useAuthStore } from '@/stores/authStore';
// ...
const state = useAuthStore.getState();
const user = state.auth.user;
const isOwner = user?.isOwner ?? false;
const hasDashboard = isOwner || (user?.scopes ?? []).includes('read_dashboard');
if (!hasDashboard) {
  throw redirect({ to: '/project/playground', replace: true });
}
```

- [ ] **Step 3: 手动验证**

注册用户登录后浏览器访问 `/`：应立即跳到 `/project/playground`，不再显示 dashboard 错误页。owner 访问 `/` 正常进 dashboard。

---

### Task 2: 修复 `apiKeyTokenUsageStats` 权限（认项目级 scope）

**Files:**
- Modify: `internal/server/gql/dashboard.resolvers.go:423-426`（`APIKeyTokenUsageStats` resolver）
- Test: `internal/server/gql/dashboard.resolvers_test.go`（新建或追加）

当前该 resolver 用 `authz.RequireScope(ctx, scopes.ScopeReadAPIKeys)`，因 `RequireScope` 不认项目级 scope，注册用户被拒。改为：从 ctx 取 projectID，用 `RunWithScopeDecision(ctx, ScopeReadAPIKeys)` 让 ent privacy 的项目级规则放行，再按 projectID 过滤。

- [ ] **Step 1: 读现有 resolver 与 service 方法**

Run: 读 `dashboard.resolvers.go:420-470`（`APIKeyTokenUsageStats` 完整方法体），确认它调用的 service 方法与查询结构；读 `internal/server/biz/` 里 `APIKeyTokenUsageStats`/`ApiKeyTokenUsageStats` 对应的 biz 方法签名。

- [ ] **Step 2: 写失败测试**

在 `internal/server/gql/dashboard.resolvers_test.go`（package `gql`，若不存在则新建；先读同目录已有 `*_test.go` 的 setup 模式——通常是构造 `Resolver{...}` + enttest client + 注入 user principal）。追加测试：注册用户带 `X-Project-ID`（个人项目）调用 `APIKeyTokenUsageStats`，应成功返回该项目的统计，而非权限错误。

```go
func TestAPIKeyTokenUsageStats_RegularUserOwnProject(t *testing.T) {
	// setup: 初始化系统、注册一个普通用户 + 其个人项目、为该项目建一把 api key + 一条 usage log
	resolver, client, regularUser, projectID := setupUsageTest(t)
	defer client.Close()

	// 构造 ctx：注入普通用户 principal + projectID
	ctx := contexts.WithUser(authz.WithPrincipal(ent.NewContext(context.Background(), client), authz.Principal{Type: authz.PrincipalTypeUser, UserID: &regularUser.ID}), regularUser)
	ctx = contexts.WithProjectID(ctx, projectID)

	stats, err := resolver.APIKeyTokenUsageStats(ctx, /* 按方法实际参数 */)
	require.NoError(t, err)
	require.NotEmpty(t, stats)
}
```

`setupUsageTest` 与参数按同目录现有测试 helper 风格实现（参照 `internal/server/biz/registration_test.go` 的 `setupTestRegistrationService` 用 `enttest.NewEntClient` + `systemService.Initialize` + `Register` 建普通用户）。`APIKeyTokenUsageStats` 的实参（如 timeWindow）以 Step 1 读到的签名为准。

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/server/gql/ -run TestAPIKeyTokenUsageStats_RegularUserOwnProject -v`
Expected: FAIL——返回 `does not have required scope read_api_keys`。

- [ ] **Step 4: 改 resolver**

把 `dashboard.resolvers.go:424` 的：

```go
	if err := authz.RequireScope(ctx, scopes.ScopeReadAPIKeys); err != nil {
		return nil, err
	}
```

改为用 scope decision + projectID 过滤（保留对 owner 的全量行为）：

```go
	projectID, hasProject := contexts.GetProjectID(ctx)
	if !hasProject {
		return nil, fmt.Errorf("project id is required")
	}

	return authz.RunWithScopeDecision(ctx, scopes.ScopeReadAPIKeys, func(scopeCtx context.Context) (res []model.YourResultType, err error) {
		// 调用 biz 方法，传入 projectID 作为过滤；biz 内部用 scopeCtx 查询，
		// privacy 的 UserProjectScopeReadRule 会校验 user 在 projectID 的 read_api_keys。
		return r.someService.APIKeyTokenUsageStats(scopeCtx, projectID /* 其余参数 */)
	})
```

注意：① 返回类型与方法名以 Step 1 读到的实际为准。② 若现有 biz 方法不接受 projectID 参数，需在 biz 方法里加 `WhereAPIKeyHasProjectWith(project.IDEQ(projectID))`（或对应 edge），用 `scopeCtx` 查询。③ `RunWithScopeDecision` 是泛型 `RunWithScopeDecision[T]`（`authz/scope.go:34`），返回 `(T, error)`，类型推断即可。确认 import 含 `"github.com/looplj/axonhub/internal/contexts"`、`"fmt"`。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/server/gql/ -run TestAPIKeyTokenUsageStats_RegularUserOwnProject -v`
Expected: PASS。

---

### Task 3: 新增「我的用量」聚合 query + `/project/usage` 看板

让注册用户在选中的个人项目内看到自己的用量聚合（总请求/总 token/成功率 + 按天趋势），数据严格限定在该项目。

**Files:**
- Modify: `internal/server/gql/dashboard.graphql`
- Modify: `internal/server/gql/dashboard.resolvers.go`
- Create: `frontend/src/routes/_authenticated/project/usage.tsx`
- Create: `frontend/src/features/project-usage/data/project-usage.ts`
- Create: `frontend/src/features/project-usage/components/project-usage-overview.tsx`
- Create: `frontend/src/features/project-usage/index.tsx`

- [ ] **Step 1: 定义 GraphQL schema**

在 `internal/server/gql/dashboard.graphql` 追加（`TimeWindow` 与现有 dashboard query 复用同一 enum）：

```graphql
type ProjectUsageOverview {
  totalRequests: Int!
  totalTokens: Int!
  successRate: Float!
  todayRequests: Int!
}

extend type Query {
  projectUsageOverview(timeWindow: TimeWindow): ProjectUsageOverview!
}
```

- [ ] **Step 2: 生成代码**

Run: `make generate`
Expected: 成功；`dashboard.resolvers.go` 出现 `ProjectUsageOverview` resolver 桩。

- [ ] **Step 3: 实现 resolver（项目级 scope）**

```go
func (r *Resolver) ProjectUsageOverview(ctx context.Context, timeWindow *model.TimeWindow) (*model.ProjectUsageOverview, error) {
	projectID, ok := contexts.GetProjectID(ctx)
	if !ok {
		return nil, fmt.Errorf("project id is required")
	}

	return authz.RunWithScopeDecision(ctx, scopes.ScopeReadRequests, func(scopeCtx context.Context) (*model.ProjectUsageOverview, error) {
		_, to := parseTimeWindowOrDefault(timeWindow) // 复用 dashboard_helpers.go 的窗口解析；函数名以实际为准

		client := r.client
		reqs, err := client.Request.Query().
			Where(request.HasProjectWith(project.IDEQ(projectID))).
			Where(request.CreatedAtGTE(to.From)).
			Count(scopeCtx)
		if err != nil {
			return nil, err
		}

		// 其余指标同理：用 client.UsageLog.Query().Where(usagelog.HasProjectWith(...)) 聚合 totalTokens；
		// successRate = completed / total；todayRequests 用 today 窗口再 Count 一次。
		// 隐私由 WithScopeDecision(ScopeReadRequests) + projectID 保证：仅该 project 且 user 有 read_requests 才放行。

		return &model.ProjectUsageOverview{
			TotalRequests: reqs,
			// ...其余字段
		}, nil
	})
}
```

注意：① `parseTimeWindowOrDefault`/时间窗口 helper 以 `dashboard_helpers.go` 实际函数名为准——先读该文件确认。② `model.ProjectUsageOverview`、`model.TimeWindow` 是 `make generate` 后的类型（`internal/server/gql/models_gen.go`）。③ 三个指标的完整聚合代码按 dashboard 现有 `dashboardOverview` resolver 的写法补齐（同一文件可参照）。④ import 按需补 `request`、`project`、`usagelog` ent 包。

- [ ] **Step 4: 后端单测（注册用户能查自己项目，查不到别人项目）**

参照 Task 2 的 setup，断言：注册用户带自己 projectID 能拿到非零统计；带 owner Default 项目的 projectID 则 privacy deny（返回错误或空）。

Run: `go test ./internal/server/gql/ -run TestProjectUsageOverview -v`
Expected: PASS。

- [ ] **Step 5: 前端 data hook**

新建 `frontend/src/features/project-usage/data/project-usage.ts`，用 `graphqlRequest` 查 `projectUsageOverview`，传 `X-Project-ID` header（参照 `frontend/src/features/dashboard/data/dashboard.ts` 的 `graphqlRequest` 用法）。导出 `useProjectUsageOverview`：

```ts
import { graphqlRequest } from '@/gql/graphql';
import { useQuery } from '@tanstack/react-query';
import { useSelectedProjectId } from '@/stores/projectStore';

export const PROJECT_USAGE_OVERVIEW_QUERY = /* GraphQL */ `
  query ProjectUsageOverview($timeWindow: TimeWindow) {
    projectUsageOverview(timeWindow: $timeWindow) {
      totalRequests
      totalTokens
      successRate
      todayRequests
    }
  }
`;

export function useProjectUsageOverview(timeWindow?: string) {
  const projectId = useSelectedProjectId();
  return useQuery({
    queryKey: ['projectUsageOverview', projectId, timeWindow],
    enabled: !!projectId,
    queryFn: () => graphqlRequest(PROJECT_USAGE_OVERVIEW_QUERY, { timeWindow }, { projectId }),
    refetchInterval: 30_000,
  });
}
```

`graphqlRequest` 的第三参（headers/projectId）以 `dashboard.ts` 现有调用形状为准——先读该文件确认传 `X-Project-ID` 的方式。

- [ ] **Step 6: 前端看板组件**

新建 `frontend/src/features/project-usage/index.tsx`，用 recharts（参照 `features/dashboard/components/`）渲染概览卡片 + 按天趋势图（按天趋势可复用 `dailyRequestStats`，但需新增 project 过滤版本；为控制范围，首版只做概览 4 卡片 + 现有 `dailyRequestStats` 的 owner 视图不混入）。注册用户看到自己项目的总请求/总 token/成功率/今日请求。

新建路由 `frontend/src/routes/_authenticated/project/usage.tsx`：

```tsx
import { createFileRoute } from '@tanstack/react-router';
import { ProjectUsage } from '@/features/project-usage';

export const Route = createFileRoute('/_authenticated/project/usage')({
  component: ProjectUsage,
});
```

- [ ] **Step 7: 加菜单入口**

在 `frontend/src/sidebar.ts` 的 Project 组（与 API Keys/Requests 同组，需 `read_requests`）加一项指向 `/project/usage`，label 走 i18n（`sidebar.usage`）。在 `frontend/src/locales/{en,zh-CN}/base.json` 加 `sidebar.usage` = "Usage" / "用量"。

- [ ] **Step 8: 手动验证**

注册用户登录 → 选个人项目 → sidebar「用量」→ 看到自己的用量卡片。owner 切到任一项目也能看该项目的用量。

---

### Task 4: 模型可用性脱敏面板（可见可用性，不见渠道配置）

**Files:**
- Modify: `internal/server/gql/dashboard.graphql`
- Create: `internal/server/biz/model_availability.go`
- Modify: `internal/server/gql/dashboard.resolvers.go`（新增 `ModelAvailability` resolver）
- Create: `frontend/src/features/playground/components/model-availability-panel.tsx`

让注册用户看到「哪些模型可用、成功率、大致延迟、能力（流式/视觉/工具/推理）」，**不暴露渠道名/provider/凭证/价格**。

- [ ] **Step 1: 定义 GraphQL schema**

在 `dashboard.graphql` 追加：

```graphql
type ModelCapabilities {
  streaming: Boolean
  vision: Boolean
  toolCall: Boolean
  reasoning: Boolean
}

type ModelAvailability {
  modelId: String!
  displayName: String!
  available: Boolean!
  successRate: Float
  avgLatencyMs: Float
  capabilities: ModelCapabilities
}

extend type Query {
  modelAvailability: [ModelAvailability!]!
}
```

- [ ] **Step 2: 生成代码**

Run: `make generate`
Expected: 成功；生成 `ModelAvailability`/`ModelCapabilities` 类型与 `ModelAvailability` resolver 桩。

- [ ] **Step 3: 后端聚合 service**

新建 `internal/server/biz/model_availability.go`，`ListModelAvailability(ctx)` 用 `RunWithSystemBypass` 聚合：

```go
func (s *ModelService) ListModelAvailability(ctx context.Context) ([]*ModelAvailabilityInfo, error) {
	return authz.RunWithSystemBypass(ctx, "model-availability", func(bypassCtx context.Context) ([]*ModelAvailabilityInfo, error) {
		models, err := s.ListEnabledModels(bypassCtx) // 复用，返回 ModelFacade{ID,DisplayName,...}
		if err != nil {
			return nil, err
		}

		// 从 Model 实体读 ModelCard 能力（vision/toolCall/streaming/reasoning）；
		// successRate / avgLatencyMs：参照 dashboard.resolvers.go 的 fastestModels/modelPerformanceStats
		// 按 model_id 聚合 Request 表（completed vs total、metrics_latency_ms 均值）。
		// 关键：输出绝不包含 channel/provider/price，只含 modelId + 聚合指标 + 能力。

		result := make([]*ModelAvailabilityInfo, 0, len(models))
		for _, m := range models {
			result = append(result, &ModelAvailabilityInfo{
				ModelID:     m.ID,
				DisplayName: m.DisplayName,
				Available:   true,
				// SuccessRate / AvgLatencyMs / Capabilities 按上面聚合填充
			})
		}
		return result, nil
	})
}
```

`ModelAvailabilityInfo` 放 `internal/objects/` 或本文件，字段对齐 GraphQL type。聚合的 raw SQL/ent 写法参照 `dashboard_helpers.go`（`buildDateExpression`、`singleflight` 缓存模式）。**先读 `model.go` 的 `ListEnabledModels`、`ModelFacade`、`ModelCard` 结构与 `dashboard.resolvers.go` 里 `fastestModels` 的聚合代码**，照搬其 ent 查询模式实现 successRate/avgLatency。

- [ ] **Step 4: resolver 接线**

```go
func (r *Resolver) ModelAvailability(ctx context.Context) ([]*model.ModelAvailability, error) {
	items, err := r.modelService.ListModelAvailability(ctx)
	if err != nil {
		return nil, err
	}
	// 映射 ModelAvailabilityInfo -> *model.ModelAvailability
}
```

确认 resolver struct 已注入 `ModelService`（`r.modelService`）；若字段名不同以实际为准。该 query 不加 `RequireScope`——任何登录用户（含注册用户）可读，因为输出已脱敏。

- [ ] **Step 5: 后端测试**

断言 `ListModelAvailability` 输出只含 modelId/displayName/available/指标/能力，断言不含 channel 字段；owner 与注册用户都能拿到。

Run: `go test ./internal/server/biz/ -run TestListModelAvailability -v`
Expected: PASS。

- [ ] **Step 6: 前端面板**

新建 `frontend/src/features/playground/components/model-availability-panel.tsx`，用 `graphqlRequest` 查 `modelAvailability`，recharts 表格/卡片列出模型 + 成功率 + 延迟 + 能力图标。在 playground 页或 `/project` 下加一个入口（如 playground 侧栏「模型可用性」按钮打开 Dialog，或独立 tab）。先读 `features/playground/index.tsx` 确认插入点。

- [ ] **Step 7: 手动验证**

注册用户能看到模型可用性面板（模型名、成功率、延迟、能力），看不到任何渠道/provider 信息。owner 看到相同脱敏视图。

---

### Task 5: 全量回归

- [ ] **Step 1: 后端**

Run: `go test ./internal/server/biz/ ./internal/server/gql/`
Expected: PASS。

- [ ] **Step 2: 前端类型**

Run: `cd frontend && npx tsc --noEmit`
Expected: 无错。

- [ ] **Step 3: 手动端到端**

注册用户：登录→playground→用量页（见自己数据）→模型可用性面板（脱敏）→API Keys 列表点 token 图表（不再报权限错）。owner：全部功能正常，dashboard 不受影响。

---

## Self-Review 结论

- **Spec 覆盖**：用户需求1 = "注册用户体验更好，如渠道可用性查看但不可见配置"。Task 4（模型可用性脱敏面板）直接对应；Task 1/2/3 补齐注册用户当前不可用的体验（dashboard 误入、key 用量、个人用量看板）。"渠道可用性"严格脱敏为"模型可用性"——不暴露渠道身份，符合"不能查看渠道配置"。
- **占位符扫描**：Task 3/4 的后端聚合因依赖现有 dashboard helper 的具体函数名，已注明"先读 dashboard_helpers.go / model.go 确认"，避免凭空造代码；这是 setup 类指引而非占位符。
- **类型一致性**：`RunWithScopeDecision` 泛型在 Task 2/3 复用；`projectUsageOverview`/`modelAvailability` 的 schema type 与 resolver/service 返回类型对齐。
- **Scope 风险**：Task 4 是本计划最大块（新 service + 聚合）；若希望更小步交付，可先合 Task 1-3（纯体验修复，低风险），Task 4 单独迭代。
