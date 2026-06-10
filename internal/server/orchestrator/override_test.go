package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestParamRulesSetDeleteAndStringTransforms(t *testing.T) {
	out, err := applyParamOverride(
		[]byte(`{"model":"openai/GPT-5","temperature":1,"messages":[{"content":"  hello  "}],"drop":"x"}`),
		overrideMap(t, `{
			"operations": [
				{"mode": "set", "path": "temperature", "value": 0.2},
				{"mode": "delete", "path": "drop"},
				{"mode": "trim_space", "path": "messages.0.content"},
				{"mode": "to_lower", "path": "model"}
			]
		}`),
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "openai/gpt-5", gjson.GetBytes(out, "model").String())
	require.Equal(t, 0.2, gjson.GetBytes(out, "temperature").Float())
	require.Equal(t, "hello", gjson.GetBytes(out, "messages.0.content").String())
	require.False(t, gjson.GetBytes(out, "drop").Exists())
}

func TestParamRulesRequireOperationsObject(t *testing.T) {
	_, err := applyParamOverride([]byte(`{"model":"gpt-5"}`), overrideMap(t, `{"temperature":0.2}`), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "operations are required")
}

func TestParamRulesWildcardAndNegativeIndex(t *testing.T) {
	out, err := applyParamOverride(
		[]byte(`{"messages":[{"content":"a"},{"content":"b"}],"tools":[{"name":"one"},{"name":"two"}]}`),
		overrideMap(t, `{
			"operations": [
				{"mode": "append", "path": "messages.-1.content", "value": "!"},
				{"mode": "set", "path": "tools.*.enabled", "value": true}
			]
		}`),
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "b!", gjson.GetBytes(out, "messages.1.content").String())
	require.True(t, gjson.GetBytes(out, "tools.0.enabled").Bool())
	require.True(t, gjson.GetBytes(out, "tools.1.enabled").Bool())
}

func TestParamRulesConditionContextAndRetry(t *testing.T) {
	ctx := map[string]any{
		"retry_index":            1,
		"last_error_status_code": 429,
	}
	out, err := applyParamOverride(
		[]byte(`{"model":"gpt-5","temperature":0.7}`),
		overrideMap(t, `{
			"operations": [
				{
					"mode": "set",
					"path": "temperature",
					"value": 0,
					"logic": "AND",
					"conditions": [
						{"path": "retry_index", "mode": "gte", "value": 1},
						{"path": "last_error_status_code", "mode": "full", "value": 429}
					]
				}
			]
		}`),
		ctx,
	)
	require.NoError(t, err)
	require.Equal(t, 0.0, gjson.GetBytes(out, "temperature").Float())
}

func TestParamRulesPlaceholdersApplyToBodyAndHeaders(t *testing.T) {
	ctx := map[string]any{
		"model":           "provider/gpt-5",
		"api_key":         "provider-key",
		"request_headers": map[string]any{"x-client": "client-value"},
		"retry":           map[string]any{"index": 2},
	}
	out, err := applyParamOverride(
		[]byte(`{"model":"gpt-5","metadata":{}}`),
		overrideMap(t, `{
			"operations": [
				{"mode": "set", "path": "metadata.client", "value": "{client_header:X-Client}"},
				{"mode": "set", "path": "metadata.api_key", "value": "{api_key}"},
				{"mode": "set", "path": "metadata.model", "value": "{model}"},
				{"mode": "set", "path": "metadata.retry_index", "value": "{retry.index}"},
				{"mode": "set_header", "path": "X-Client", "value": "{client_header:X-Client}"},
				{"mode": "set_header", "path": "X-Model", "value": "{model}"}
			]
		}`),
		ctx,
	)
	require.NoError(t, err)
	require.Equal(t, "client-value", gjson.GetBytes(out, "metadata.client").String())
	require.Equal(t, "provider-key", gjson.GetBytes(out, "metadata.api_key").String())
	require.Equal(t, "provider/gpt-5", gjson.GetBytes(out, "metadata.model").String())
	require.Equal(t, "2", gjson.GetBytes(out, "metadata.retry_index").String())
	headers, ok := ctx["runtime_request_headers"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "client-value", headers["x-client"])
	require.Equal(t, "provider/gpt-5", headers["x-model"])
}

func TestParamRulesPlaceholdersHandleEmptyContext(t *testing.T) {
	out, err := applyParamOverride(
		[]byte(`{"metadata":{}}`),
		overrideMap(t, `{
			"operations": [
				{"mode": "set", "path": "metadata.api_key", "value": "{api_key}"},
				{"mode": "set", "path": "metadata.client", "value": "{client_header:X-Client}"}
			]
		}`),
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "", gjson.GetBytes(out, "metadata.api_key").String())
	require.Equal(t, "", gjson.GetBytes(out, "metadata.client").String())
}

func TestParamRulesReturnError(t *testing.T) {
	_, err := applyParamOverride(
		[]byte(`{"model":"gpt-5"}`),
		overrideMap(t, `{
			"operations": [
				{"mode": "return_error", "value": {"message": "blocked", "status_code": 403, "code": "blocked_by_rule"}}
			]
		}`),
		nil,
	)
	require.Error(t, err)
	var returnErr *paramOverrideReturnError
	require.True(t, errors.As(err, &returnErr))
	require.Equal(t, "blocked", returnErr.Message)
	require.Equal(t, http.StatusForbidden, returnErr.StatusCode)
	require.Equal(t, "blocked_by_rule", returnErr.Code)
}

func TestOpenAIParamOverrideHTTPErrorSkipRetry(t *testing.T) {
	err := openAIParamOverrideHTTPError(&paramOverrideReturnError{
		Message:    "rate limited",
		StatusCode: http.StatusTooManyRequests,
		SkipRetry:  true,
	})

	var httpErr *httpclient.Error
	require.True(t, errors.As(err, &httpErr))
	require.Equal(t, http.StatusTooManyRequests, httpErr.StatusCode)
	require.True(t, isParamOverrideSkipRetryError(err))

	channel := &biz.Channel{
		Channel:  &ent.Channel{ID: 1, Name: "current"},
		Outbound: &mockTransformer{},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidateIndex: 0,
			CurrentCandidate: &ChannelModelsCandidate{
				Channel: channel,
				Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-5", ActualModel: "gpt-5"}},
			},
			ChannelModelsCandidates: []*ChannelModelsCandidate{
				{
					Channel: channel,
					Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-5", ActualModel: "gpt-5"}},
				},
				{
					Channel: &biz.Channel{Channel: &ent.Channel{ID: 2, Name: "next"}, Outbound: &mockTransformer{}},
					Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-5", ActualModel: "gpt-5"}},
				},
			},
		},
	}

	require.False(t, outbound.CanRetry(err))
	require.False(t, outbound.HasMoreChannels())
}

func TestOpenAIParamOverrideHTTPErrorAllowsRetryWhenConfigured(t *testing.T) {
	err := openAIParamOverrideHTTPError(&paramOverrideReturnError{
		Message:    "rate limited",
		StatusCode: http.StatusTooManyRequests,
		SkipRetry:  false,
	})

	channel := &biz.Channel{
		Channel:  &ent.Channel{ID: 1, Name: "current"},
		Outbound: &mockTransformer{},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{
				Channel: channel,
				Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-5", ActualModel: "gpt-5"}},
			},
		},
	}

	require.False(t, isParamOverrideSkipRetryError(err))
	require.True(t, outbound.CanRetry(err))
}

func TestParamOverrideContextUsesSelectedChannelAPIKey(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:          1,
			Name:        "multi-key",
			Credentials: objects.ChannelCredentials{APIKeys: []string{"first-key", "selected-key"}},
		},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{
				Channel: channel,
				Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-5", ActualModel: "provider/gpt-5"}},
			},
		},
	}
	requestCtx := contexts.WithChannelAPIKey(context.Background(), "selected-key")
	overrideCtx := buildParamOverrideContext(requestCtx, outbound, &httpclient.Request{})

	out, err := applyParamOverride(
		[]byte(`{"metadata":{}}`),
		overrideMap(t, `{
			"operations": [
				{"mode": "set", "path": "metadata.api_key", "value": "{api_key}"},
				{"mode": "set_header", "path": "X-API-Key", "value": "{api_key}"}
			]
		}`),
		overrideCtx,
	)
	require.NoError(t, err)
	require.Equal(t, "selected-key", gjson.GetBytes(out, "metadata.api_key").String())
	headers, ok := overrideCtx["runtime_request_headers"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "selected-key", headers["x-api-key"])
}

func TestParamOverrideMiddlewareRuntimeRequestHeaders(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "runtime-header-test",
			Settings: &objects.ChannelSettings{
				ParamOverride: `{
					"operations": [
						{"mode": "pass_headers", "value": ["X-Trace-Id"]},
						{"mode": "set_header", "path": "X-Static", "value": "static"},
						{"mode": "delete_header", "path": "X-Remove"}
					]
				}`,
			},
		},
		Outbound: &mockTransformer{},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest: &llm.Request{
				RawRequest: &httpclient.Request{
					Headers: http.Header{"X-Trace-Id": []string{"trace-123"}},
				},
			},
		},
	}

	bodyMiddleware := applyOverrideRequestBody(outbound)
	request := &httpclient.Request{Body: []byte(`{"model":"gpt-5"}`)}
	request, err := bodyMiddleware.OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)

	headerMiddleware := applyOverrideRequestHeaders(outbound)
	request.Headers = http.Header{"Authorization": []string{"Bearer original"}}
	request.Auth = &httpclient.AuthConfig{Type: httpclient.AuthTypeBearer, APIKey: "provider-key"}
	request, err = headerMiddleware.OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)

	require.Equal(t, "trace-123", request.Headers.Get("X-Trace-Id"))
	require.Equal(t, "static", request.Headers.Get("X-Static"))
	require.Empty(t, request.Headers.Get("X-Remove"))
}

func TestParamOverrideHeaderFinalResolution(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "param-override-header-test",
			Settings: &objects.ChannelSettings{
				ParamOverride: `{
					"operations": [
						{"mode": "set_header", "path": "*", "value": "{client_header:X-Allowed}"},
						{"mode": "set_header", "path": "Authorization", "value": "Bearer custom"},
						{"mode": "set_header", "path": "X-API-Key", "value": "{api_key}"},
						{"mode": "set_header", "path": "X-Client", "value": "{client_header:X-Client}"}
					]
				}`,
			},
			Credentials: objects.ChannelCredentials{APIKey: "provider-key"},
		},
		Outbound: &mockTransformer{},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest: &llm.Request{
				RawRequest: &httpclient.Request{
					Headers: http.Header{
						"X-Allowed": []string{"allowed"},
						"X-Client":  []string{"client"},
					},
				},
			},
		},
	}

	request := &httpclient.Request{
		Body:    []byte(`{"model":"gpt-5"}`),
		Headers: http.Header{},
		Auth:    &httpclient.AuthConfig{Type: httpclient.AuthTypeBearer, APIKey: "provider-key"},
	}
	bodyMiddleware := applyOverrideRequestBody(outbound)
	request, err := bodyMiddleware.OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)

	middleware := applyOverrideRequestHeaders(outbound)
	request, err = middleware.OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)

	require.Equal(t, "allowed", request.Headers.Get("X-Allowed"))
	require.Equal(t, "Bearer custom", request.Headers.Get("Authorization"))
	require.Equal(t, "provider-key", request.Headers.Get("X-API-Key"))
	require.Equal(t, "client", request.Headers.Get("X-Client"))
}

func TestParamOverridePruneObjectsAndSyncFields(t *testing.T) {
	out, err := applyParamOverride(
		[]byte(`{"metadata":{"trace_id":"abc"},"messages":[{"type":"text","content":"keep"},{"type":"debug","content":"drop"}]}`),
		overrideMap(t, `{
			"operations": [
				{"mode": "prune_objects", "path": "messages", "value": {"where": {"type": "debug"}}},
				{"mode": "sync_fields", "from": "json:metadata.trace_id", "to": "header:X-Trace-Id"}
			]
		}`),
		map[string]any{},
	)
	require.NoError(t, err)
	require.Equal(t, 1, len(gjson.GetBytes(out, "messages").Array()))
}

func overrideMap(t *testing.T, raw string) map[string]any {
	t.Helper()
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &result))
	return result
}
