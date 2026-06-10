# Parameter Override Guide

Parameter override is a channel-level feature that rewrites the outbound request before it is sent to the provider. There is one configuration entry: `settings.paramOverride`. It is a JSON object with an `operations` array.

Request body changes and request header changes live in the same parameter override document.

## Configuration Shape

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

Each item in `operations` is evaluated in order. An operation runs when all of its `conditions` pass for `logic: "AND"`, or when at least one condition passes for `logic: "OR"`. Missing `logic` defaults to `OR`. An operation with no conditions always runs.

## Variable Scope

All string values in the same parameter override document share the same placeholder syntax. This includes request body operations, request header operations, condition values, `from`, `to`, object values, and array values.

| Placeholder | Scope | Meaning |
| :--- | :--- | :--- |
| `{model}` | Body and header rules | Upstream model currently selected for the channel. Falls back to the original model when no upstream model is available. |
| `{upstream_model}` | Body and header rules | Same upstream model value as `{model}` when a channel candidate is selected. |
| `{original_model}` | Body and header rules | Model from the original client request before channel model mapping. |
| `{request_path}` | Body and header rules | Current request path when available. |
| `{api_key}` | Body and header rules | Provider API key selected for the current channel execution. |
| `{retry_index}` | Body and header rules | Current retry index, starting at `0`. |
| `{is_retry}` | Body and header rules | `true` when `retry_index > 0`, otherwise `false`. |
| `{last_error_status_code}` | Body and header rules | HTTP status code from the previous failed attempt, available during retry. |
| `{last_error_message}` | Body and header rules | Raw previous error body, available during retry. |
| `{client_header:Name}` | Body and header rules | Value of the original inbound client request header `Name`. Header lookup is case-insensitive. Missing headers resolve to an empty string. |
| `{request_header:Name}` | Body and header rules | Current runtime request header `Name`, then original inbound client header `Name` as fallback. |
| `{header:Name}` | Body and header rules | Alias of `{request_header:Name}`. |
| `{retry.index}` | Body and header rules | Nested path lookup from the runtime context. Equivalent to `{retry_index}`. |

The `request_headers` context map contains original inbound client headers with lowercase names. The `runtime_request_headers` context map contains headers staged by `set_header`, `pass_headers`, `copy_header`, `move_header`, and `sync_fields` during parameter override execution.

## Paths And Matching

Body paths use dot notation. Wildcard `*` expands over matching object keys or array indexes. Negative indexes are supported for array paths in the form `.-1`, `.-2`, and so on.

Examples:

| Path | Scope |
| :--- | :--- |
| `temperature` | Top-level field. |
| `messages.0.content` | First message content. |
| `messages.-1.content` | Last message content. |
| `tools.*.enabled` | `enabled` field on every tool item. |

Header names are case-insensitive and normalized to lowercase internally. Outbound HTTP headers are written through Go's `http.Header`, so final wire casing is canonicalized by the HTTP stack.

## Conditions

Conditions can read both request body fields and runtime context variables. Body fields have priority. When the path is missing from the body, the condition reads the runtime context.

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

Condition fields:

| Field | Required | Scope |
| :--- | :--- | :--- |
| `path` | Yes | Request body path first, runtime context path second. |
| `mode` | Yes | `full`, `prefix`, `suffix`, `contains`, `gt`, `gte`, `lt`, `lte`. |
| `value` | No | Comparison value. Placeholder variables are resolved before comparison. |
| `invert` | No | Reverses the comparison result. |
| `pass_missing_key` | No | Treats a missing path as a passed condition. |

Numeric comparison modes require both compared values to be numbers.

## Body Operations

These operations modify the JSON request body.

| Mode | Required Fields | Effect |
| :--- | :--- | :--- |
| `set` | `path`, `value` | Writes `value` to `path`. Creates missing fields. `keep_origin: true` skips the write when the target exists. |
| `delete` | `path` | Deletes `path`. |
| `append` | `path`, `value` | Appends to an array, appends to a string, or merges object fields into an object. |
| `prepend` | `path`, `value` | Prepends to an array or string, or merges object fields into an object. |
| `copy` | `from`, `to` | Copies a body value from `from` to `to`. |
| `move` | `from`, `to` | Moves a body value from `from` to `to`. |
| `replace` | `path`, `from`, `to` | Replaces all string matches in the target string. |
| `regex_replace` | `path`, `from`, `to` | Replaces regex matches in the target string. |
| `trim_prefix` | `path`, `value` | Removes a string prefix. |
| `trim_suffix` | `path`, `value` | Removes a string suffix. |
| `ensure_prefix` | `path`, `value` | Adds a string prefix when missing. |
| `ensure_suffix` | `path`, `value` | Adds a string suffix when missing. |
| `trim_space` | `path` | Trims leading and trailing whitespace on a string. |
| `to_lower` | `path` | Converts a string to lowercase. |
| `to_upper` | `path` | Converts a string to uppercase. |
| `prune_objects` | `value`; `path` optional | Removes matching objects from the whole body or from the body node at `path`. |
| `return_error` | `value` | Stops execution and returns a custom HTTP error to the client. |

## Request Header Operations

These operations modify the outbound request headers as part of parameter override. They do not use a separate configuration field.

| Mode | Required Fields | Effect |
| :--- | :--- | :--- |
| `pass_headers` | `value` | Copies original inbound client headers to the outbound request. `value` can be a comma-separated string, JSON array, or object with `headers`, `names`, or `header`. |
| `set_header` | `path`, `value` | Sets an outbound request header. An empty resolved value removes the staged header. `keep_origin: true` preserves an existing staged value. |
| `delete_header` | `path` | Deletes a staged outbound request header. |
| `copy_header` | `from`, `to` | Copies a request header from runtime/original header context to a staged outbound header. |
| `move_header` | `from`, `to` | Copies a request header and removes the source staged header. |
| `sync_fields` | `from`, `to` | Copies a value between body and headers when one side exists and the other is missing. Use `json:path` or `header:name`. Missing prefix means `json`. |

Request header operations run during body override first, stage their output in `runtime_request_headers`, and are applied to the real outbound HTTP request after auth headers are finalized.

## Header Mapping Values

`set_header` can accept either a string value or a mapping object. String values support the same placeholders as body values.

```json
{
  "mode": "set_header",
  "path": "X-Client",
  "value": "{client_header:X-Client}"
}
```

Mapping objects are designed for comma-separated token headers such as `anthropic-beta`.

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

Mapping keys match existing comma-separated source tokens. A string replacement inserts the replacement token. `null` removes the token. `$append` appends tokens. `$keep_only_declared: true` removes source tokens that are not declared in the mapping.

## `prune_objects`

`prune_objects` removes objects that match conditions. When `path` is empty, the entire request body is searched. When `path` is set, only that body node is searched.

Simple form:

```json
{
  "mode": "prune_objects",
  "path": "messages",
  "value": "redacted_thinking"
}
```

Advanced form:

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

`type` is shorthand for condition `{ "path": "type", "mode": "full" }`. `where` is shorthand for exact-match conditions. `recursive: false` checks only the current level.

## `return_error`

`return_error` immediately stops request processing and returns an OpenAI-compatible error body.

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

`value` can also be a string, which becomes the error message. `status_code` defaults to `400`; `code` defaults to `invalid_request`; `type` defaults to `invalid_request_error`; `skip_retry` defaults to `true`.

## Examples

Use a client header in both body and outbound request header:

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

Pass Claude/Codex CLI headers:

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

Sync body metadata into a request header:

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

Retry-specific parameter change:

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
