# 参数覆盖指南

参数覆盖是渠道级功能，用于在请求发送给上游服务商前改写出站请求。唯一配置入口是 `settings.paramOverride`，内容是一个包含 `operations` 数组的 JSON 对象。

请求体修改和请求头修改都写在同一个参数覆盖文档里。独立的请求头覆盖配置已经删除。

## 配置格式

```json
{
  "operations": [
    {
      "mode": "set",
      "path": "temperature",
      "value": 0.7,
      "conditions": [
        { "path": "model", "mode": "prefix", "value": "openai/" }
      ],
      "logic": "AND"
    }
  ]
}
```

`operations` 按顺序执行。`logic: "AND"` 表示全部条件通过时执行，`logic: "OR"` 表示任一条件通过时执行。未设置 `logic` 时默认 `OR`。未设置 `conditions` 时规则总是执行。

## 变量作用域

同一个参数覆盖文档里的所有字符串共享同一套占位符语法。作用范围包括请求体规则、请求头规则、条件值、`from`、`to`、对象值和数组值。

| 占位符 | 作用域 | 含义 |
| :--- | :--- | :--- |
| `{model}` | 请求体和请求头规则 | 当前渠道选中的上游模型。缺少上游模型时回退为原始模型。 |
| `{upstream_model}` | 请求体和请求头规则 | 已选渠道候选存在时，与 `{model}` 相同。 |
| `{original_model}` | 请求体和请求头规则 | 客户端原始请求中的模型，早于渠道模型映射。 |
| `{request_path}` | 请求体和请求头规则 | 当前请求路径，存在时可用。 |
| `{api_key}` | 请求体和请求头规则 | 当前渠道执行选中的上游 API key。 |
| `{retry_index}` | 请求体和请求头规则 | 当前重试序号，从 `0` 开始。 |
| `{is_retry}` | 请求体和请求头规则 | `retry_index > 0` 时为 `true`，否则为 `false`。 |
| `{last_error_status_code}` | 请求体和请求头规则 | 上一次失败尝试的 HTTP 状态码，仅重试时可用。 |
| `{last_error_message}` | 请求体和请求头规则 | 上一次失败尝试的原始响应体，仅重试时可用。 |
| `{client_header:Name}` | 请求体和请求头规则 | 客户端原始入站请求头 `Name` 的值。请求头查找大小写不敏感，缺失时解析为空字符串。 |
| `{request_header:Name}` | 请求体和请求头规则 | 当前运行时请求头 `Name`，缺失时回退到客户端原始入站请求头 `Name`。 |
| `{header:Name}` | 请求体和请求头规则 | `{request_header:Name}` 的别名。 |
| `{retry.index}` | 请求体和请求头规则 | 从运行时上下文读取嵌套路径，等价于 `{retry_index}`。 |

`request_headers` 上下文保存客户端原始入站请求头，请求头名称为小写。`runtime_request_headers` 上下文保存参数覆盖执行过程中由 `set_header`、`pass_headers`、`copy_header`、`move_header`、`sync_fields` 暂存的出站请求头。

## 路径和匹配范围

请求体路径使用点号语法。通配符 `*` 会展开匹配对象 key 或数组索引。数组路径支持 `.-1`、`.-2` 这类负索引。

| 路径 | 范围 |
| :--- | :--- |
| `temperature` | 顶层字段。 |
| `messages.0.content` | 第一条消息的 `content`。 |
| `messages.-1.content` | 最后一条消息的 `content`。 |
| `tools.*.enabled` | 所有 tool 项的 `enabled` 字段。 |

请求头名称大小写不敏感，内部统一为小写。最终 HTTP 出站请求头由 Go `http.Header` 写入，线上的大小写由 HTTP 栈规范化。

## 条件

条件既能读取请求体字段，也能读取运行时上下文变量。读取顺序是请求体优先；请求体路径缺失时读取运行时上下文。

```json
{
  "mode": "set",
  "path": "temperature",
  "value": 0,
  "logic": "AND",
  "conditions": [
    { "path": "retry_index", "mode": "gte", "value": 1 },
    { "path": "last_error_status_code", "mode": "full", "value": 429 }
  ]
}
```

条件字段：

| 字段 | 必填 | 范围 |
| :--- | :--- | :--- |
| `path` | 是 | 先读请求体路径，再读运行时上下文路径。 |
| `mode` | 是 | `full`、`prefix`、`suffix`、`contains`、`gt`、`gte`、`lt`、`lte`。 |
| `value` | 否 | 比较值。比较前会解析占位符变量。 |
| `invert` | 否 | 反转比较结果。 |
| `pass_missing_key` | 否 | 路径缺失时视为条件通过。 |

数值比较模式要求两侧都是数字。

## 请求体操作

这些操作修改 JSON 请求体。

| Mode | 必填字段 | 效果 |
| :--- | :--- | :--- |
| `set` | `path`, `value` | 将 `value` 写入 `path`。缺失字段会创建。`keep_origin: true` 表示目标已存在时跳过。 |
| `delete` | `path` | 删除 `path`。 |
| `append` | `path`, `value` | 对数组追加元素，对字符串追加文本，对对象合并字段。 |
| `prepend` | `path`, `value` | 对数组前置元素，对字符串前置文本，对对象合并字段。 |
| `copy` | `from`, `to` | 将请求体值从 `from` 复制到 `to`。 |
| `move` | `from`, `to` | 将请求体值从 `from` 移动到 `to`。 |
| `replace` | `path`, `from`, `to` | 对目标字符串执行普通字符串替换。 |
| `regex_replace` | `path`, `from`, `to` | 对目标字符串执行正则替换。 |
| `trim_prefix` | `path`, `value` | 删除字符串前缀。 |
| `trim_suffix` | `path`, `value` | 删除字符串后缀。 |
| `ensure_prefix` | `path`, `value` | 缺少前缀时添加前缀。 |
| `ensure_suffix` | `path`, `value` | 缺少后缀时添加后缀。 |
| `trim_space` | `path` | 删除字符串首尾空白。 |
| `to_lower` | `path` | 将字符串转为小写。 |
| `to_upper` | `path` | 将字符串转为大写。 |
| `prune_objects` | `value`；`path` 可选 | 从整个请求体或 `path` 指定节点中移除匹配对象。 |
| `return_error` | `value` | 停止执行，并向客户端返回自定义 HTTP 错误。 |

## 请求头操作

这些操作作为参数覆盖的一部分修改出站请求头。它们没有单独的配置字段。

| Mode | 必填字段 | 效果 |
| :--- | :--- | :--- |
| `pass_headers` | `value` | 将客户端原始入站请求头复制到出站请求。`value` 可以是逗号分隔字符串、JSON 数组，或包含 `headers`、`names`、`header` 的对象。 |
| `set_header` | `path`, `value` | 设置出站请求头。解析后为空字符串时移除暂存请求头。`keep_origin: true` 表示已存在暂存值时保留原值。 |
| `delete_header` | `path` | 删除暂存出站请求头。 |
| `copy_header` | `from`, `to` | 从运行时/原始请求头上下文复制请求头到暂存出站请求头。 |
| `move_header` | `from`, `to` | 复制请求头并删除源暂存请求头。 |
| `sync_fields` | `from`, `to` | 在请求体和请求头之间同步值；仅在一侧存在、另一侧缺失时写入。使用 `json:path` 或 `header:name`。缺少前缀时表示 `json`。 |

请求头操作在请求体覆盖阶段执行，结果先写入 `runtime_request_headers`；鉴权请求头完成后，再写入真实出站 HTTP 请求。

## 请求头映射值

`set_header` 的 `value` 可以是字符串，也可以是映射对象。字符串值支持与请求体相同的占位符。

```json
{
  "mode": "set_header",
  "path": "X-Client",
  "value": "{client_header:X-Client}"
}
```

映射对象用于处理 `anthropic-beta` 这类逗号分隔 token 的请求头。

```json
{
  "mode": "set_header",
  "path": "anthropic-beta",
  "value": {
    "advanced-tool-use-2025-11-20": "tool-search-tool-2025-10-19",
    "bash_20250124": null,
    "$append": ["context-1m-2025-08-07"]
  }
}
```

映射 key 匹配现有逗号分隔 token。字符串替换会插入替换 token。`null` 会删除 token。`$append` 会追加 token。`$keep_only_declared: true` 会删除映射中未声明的源 token。

## `prune_objects`

`prune_objects` 会删除符合条件的对象。`path` 为空时搜索整个请求体；`path` 有值时只搜索该请求体节点。

简单写法：

```json
{
  "mode": "prune_objects",
  "path": "messages",
  "value": "redacted_thinking"
}
```

高级写法：

```json
{
  "mode": "prune_objects",
  "path": "messages",
  "value": {
    "type": "debug",
    "logic": "AND",
    "recursive": true,
    "conditions": [
      { "path": "source", "mode": "full", "value": "internal" }
    ],
    "where": {
      "type": "debug"
    }
  }
}
```

`type` 等价于 `{ "path": "type", "mode": "full" }` 条件。`where` 是精确匹配条件的简写。`recursive: false` 表示只检查当前层。

## `return_error`

`return_error` 会立即停止请求处理，并返回 OpenAI 兼容错误体。

```json
{
  "mode": "return_error",
  "value": {
    "message": "blocked by policy",
    "status_code": 403,
    "code": "blocked_by_rule",
    "type": "invalid_request_error",
    "skip_retry": true
  }
}
```

`value` 也可以是字符串，此时字符串就是错误消息。`status_code` 默认 `400`；`code` 默认 `invalid_request`；`type` 默认 `invalid_request_error`；`skip_retry` 默认 `true`。

## 示例

同时把客户端请求头写入请求体和出站请求头：

```json
{
  "operations": [
    {
      "mode": "set",
      "path": "metadata.trace_id",
      "value": "{client_header:X-Trace-Id}"
    },
    {
      "mode": "set_header",
      "path": "X-Trace-Id",
      "value": "{client_header:X-Trace-Id}"
    }
  ]
}
```

透传 Claude/Codex CLI 请求头：

```json
{
  "operations": [
    {
      "mode": "pass_headers",
      "value": ["User-Agent", "X-App", "Anthropic-Beta", "X-Codex-Beta-Features"],
      "keep_origin": true
    }
  ]
}
```

把请求体 metadata 同步到请求头：

```json
{
  "operations": [
    {
      "mode": "sync_fields",
      "from": "json:metadata.trace_id",
      "to": "header:X-Trace-Id"
    }
  ]
}
```

重试时修改参数：

```json
{
  "operations": [
    {
      "mode": "set",
      "path": "temperature",
      "value": 0,
      "logic": "AND",
      "conditions": [
        { "path": "is_retry", "mode": "full", "value": true },
        { "path": "last_error_status_code", "mode": "gte", "value": 500 }
      ]
    }
  ]
}
```
