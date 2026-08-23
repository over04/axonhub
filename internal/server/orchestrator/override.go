package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

const (
	paramOverrideContextRequestHeaders        = "request_headers"
	paramOverrideContextRuntimeRequestHeaders = "runtime_request_headers"

	headerPassthroughAllKey        = "*"
	headerPassthroughRegexPrefix   = "re:"
	headerPassthroughRegexPrefixV2 = "regex:"
)

var (
	negativeIndexRegexp       = regexp.MustCompile(`\.(-\d+)`)
	placeholderRegexp         = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_.]*(?::[^{}]+)?)\}`)
	headerPassthroughRegexMap sync.Map
	errSourceHeaderNotFound   = errors.New("source header does not exist")
)

var passthroughSkipHeaderNamesLower = map[string]struct{}{
	"connection": {}, "keep-alive": {}, "proxy-authenticate": {}, "proxy-authorization": {},
	"te": {}, "trailer": {}, "transfer-encoding": {}, "upgrade": {}, "cookie": {},
	"host": {}, "content-length": {}, "accept-encoding": {},
	"authorization": {}, "x-api-key": {}, "x-goog-api-key": {},
	"sec-websocket-key": {}, "sec-websocket-version": {}, "sec-websocket-extensions": {},
}

type conditionOperation struct {
	Path           string `json:"path"`
	Mode           string `json:"mode"`
	Value          any    `json:"value"`
	Invert         bool   `json:"invert"`
	PassMissingKey bool   `json:"pass_missing_key"`
}

type paramOperation struct {
	Path       string               `json:"path"`
	Mode       string               `json:"mode"`
	Value      any                  `json:"value"`
	KeepOrigin bool                 `json:"keep_origin"`
	From       string               `json:"from,omitempty"`
	To         string               `json:"to,omitempty"`
	Conditions []conditionOperation `json:"conditions,omitempty"`
	Logic      string               `json:"logic,omitempty"`
	// Match is the equality matcher for the array_remove mode: items of the
	// array at Path are removed when the field at Match.Path (resolved relative
	// to each item) equals Match.Eq.
	Match *paramOverrideMatch `json:"match,omitempty"`
}

// paramOverrideMatch mirrors the upstream array_remove match rule.
type paramOverrideMatch struct {
	Path string `json:"path"`
	Eq   any    `json:"eq"`
}

type paramOverrideReturnError struct {
	Message    string
	StatusCode int
	Code       string
	Type       string
	SkipRetry  bool
}

func (e *paramOverrideReturnError) Error() string {
	if e == nil || strings.TrimSpace(e.Message) == "" {
		return "param override return error"
	}
	return e.Message
}

type paramOverrideHTTPError struct {
	HTTPError *httpclient.Error
	skipRetry bool
}

func (e *paramOverrideHTTPError) Error() string {
	if e == nil || e.HTTPError == nil {
		return "param override http error"
	}
	return e.HTTPError.Error()
}

func (e *paramOverrideHTTPError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.HTTPError
}

func (e *paramOverrideHTTPError) SkipParamOverrideRetry() bool {
	return e != nil && e.skipRetry
}

func (e *paramOverrideHTTPError) SkipRetry() bool {
	return e != nil && e.skipRetry
}

type paramOverrideRetrySkipper interface {
	SkipParamOverrideRetry() bool
}

func isParamOverrideSkipRetryError(err error) bool {
	var skipper paramOverrideRetrySkipper
	return errors.As(err, &skipper) && skipper.SkipParamOverrideRetry()
}

func openAIParamOverrideHTTPError(err error) error {
	var returnErr *paramOverrideReturnError
	if !errors.As(err, &returnErr) {
		returnErr = &paramOverrideReturnError{
			Message:    err.Error(),
			StatusCode: http.StatusBadRequest,
			Code:       "invalid_request",
			Type:       "invalid_request_error",
			SkipRetry:  true,
		}
	}

	statusCode := returnErr.StatusCode
	if statusCode < http.StatusContinue || statusCode > http.StatusNetworkAuthenticationRequired {
		statusCode = http.StatusBadRequest
	}

	code := strings.TrimSpace(returnErr.Code)
	if code == "" {
		code = "invalid_request"
	}

	errType := strings.TrimSpace(returnErr.Type)
	if errType == "" {
		errType = "invalid_request_error"
	}

	message := strings.TrimSpace(returnErr.Message)
	if message == "" {
		message = "request blocked by param override"
	}

	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errType,
			"code":    code,
		},
	})

	return &paramOverrideHTTPError{
		HTTPError: &httpclient.Error{
			Method:     "POST",
			URL:        "param-override",
			StatusCode: statusCode,
			Status:     http.StatusText(statusCode),
			Body:       body,
			Headers:    make(http.Header),
		},
		skipRetry: returnErr.SkipRetry,
	}
}

func applyOverrideRequestBody(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("override-request-body", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		channel := outbound.GetCurrentChannel()
		if channel == nil {
			return request, nil
		}

		paramOverride := channel.GetParamOverrideMap()
		if len(paramOverride) == 0 {
			return request, nil
		}

		// Body override operations are implemented with gjson/sjson, which silently
		// discards a non-JSON document and rebuilds it as a fresh JSON object.
		// Applying them to a multipart body (image edit/variation, transcription,
		// ...) would replace the whole payload with a tiny JSON object while the
		// Content-Type still advertises the multipart boundary, so the upstream
		// sees a truncated form. Skip body override for non-JSON bodies.
		if !bodyOverrideSupported(request) {
			log.Warn(ctx, "skipping body override operations for non-JSON request body",
				log.String("channel", channel.Name),
				log.Int("channel_id", channel.ID),
				log.String("content_type", requestContentType(request)),
				log.String("api_format", request.APIFormat),
			)

			return request, nil
		}

		overrideCtx := buildParamOverrideContext(ctx, outbound, request)
		body, err := applyParamOverride(request.Body, paramOverride, overrideCtx)
		if err != nil {
			return nil, openAIParamOverrideHTTPError(err)
		}

		request.Body = body
		applyRuntimeRequestHeadersFromContext(request, overrideCtx)

		return request, nil
	})
}

// buildRequestHeaderMap builds a map of canonical/lowercase request headers
// for conditional candidate matching (association "when" conditions). Sensitive
// headers are excluded. This mirrors the upstream template engine's helper and
// is consumed by candidates_condition.go.
func buildRequestHeaderMap(llmReq *llm.Request) map[string]string {
	requestHeaders := make(map[string]string)
	if llmReq == nil || llmReq.RawRequest == nil || llmReq.RawRequest.Headers == nil {
		return requestHeaders
	}

	for key, values := range llmReq.RawRequest.Headers {
		if len(values) == 0 {
			continue
		}

		if httpclient.IsSensitiveHeader(key) {
			continue
		}

		value := values[0]
		canonicalKey := http.CanonicalHeaderKey(key)
		requestHeaders[canonicalKey] = value
		requestHeaders[strings.ToLower(key)] = value
	}

	return requestHeaders
}

// requestContentType returns the effective outbound Content-Type, preferring the
// explicit field and falling back to the header.
func requestContentType(request *httpclient.Request) string {
	if request == nil {
		return ""
	}

	if request.ContentType != "" {
		return request.ContentType
	}

	return request.Headers.Get("Content-Type")
}

// bodyOverrideSupported reports whether channel body override operations can
// safely be applied to this request. gjson/sjson discard non-JSON documents,
// so multipart (image edit/variation, transcription, ...) and other non-JSON
// bodies must skip body override to avoid truncating the payload.
func bodyOverrideSupported(request *httpclient.Request) bool {
	contentType := requestContentType(request)
	if contentType == "" {
		// Unknown content type: assume JSON (the historical behavior).
		return true
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}

	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func applyOverrideRequestHeaders(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("override-request-headers", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		channel := outbound.GetCurrentChannel()
		if channel == nil {
			return request, nil
		}

		if request.Headers == nil {
			request.Headers = make(http.Header)
		}

		finalized, err := httpclient.FinalizeAuthHeaders(request)
		if err != nil {
			return nil, err
		}
		request = finalized

		runtimeRequestHeaders := runtimeRequestHeadersFromRequest(request)
		if len(runtimeRequestHeaders) == 0 {
			return request, nil
		}

		resolved, err := resolveFinalRequestHeaders(outbound, runtimeRequestHeaders)
		if err != nil {
			return nil, err
		}

		for key, value := range resolved {
			request.Headers.Set(key, value)
		}

		return request, nil
	})
}

func runtimeRequestHeadersFromRequest(request *httpclient.Request) map[string]any {
	if request == nil || request.TransformerMetadata == nil {
		return nil
	}

	raw, ok := request.TransformerMetadata["runtime_request_headers"].(map[string]any)
	if !ok {
		return nil
	}

	return raw
}

func applyParamOverride(jsonData []byte, paramOverride map[string]any, conditionContext map[string]any) ([]byte, error) {
	if len(paramOverride) == 0 {
		return jsonData, nil
	}

	if operations, ok := tryParseOperations(paramOverride); ok {
		return applyOperations(jsonData, operations, conditionContext)
	}

	return nil, fmt.Errorf("param override operations are required")
}

func tryParseOperations(paramOverride map[string]any) ([]paramOperation, bool) {
	rawOps, exists := paramOverride["operations"]
	if !exists {
		return nil, false
	}

	items, ok := rawOps.([]any)
	if !ok {
		return nil, false
	}

	operations := make([]paramOperation, 0, len(items))
	for _, item := range items {
		opMap, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}

		mode, ok := opMap["mode"].(string)
		if !ok || strings.TrimSpace(mode) == "" {
			return nil, false
		}

		op := paramOperation{Mode: mode, Logic: "OR"}
		if path, ok := opMap["path"].(string); ok {
			op.Path = path
		}
		if value, exists := opMap["value"]; exists {
			op.Value = value
		}
		if keepOrigin, ok := opMap["keep_origin"].(bool); ok {
			op.KeepOrigin = keepOrigin
		}
		if from, ok := opMap["from"].(string); ok {
			op.From = from
		}
		if to, ok := opMap["to"].(string); ok {
			op.To = to
		}
		if logic, ok := opMap["logic"].(string); ok && strings.TrimSpace(logic) != "" {
			op.Logic = logic
		}
		if match, exists := opMap["match"]; exists {
			matchMap, ok := match.(map[string]any)
			if !ok {
				return nil, false
			}
			parsed := &paramOverrideMatch{}
			if path, ok := matchMap["path"].(string); ok {
				parsed.Path = path
			}
			if eq, exists := matchMap["eq"]; exists {
				parsed.Eq = eq
			}
			op.Match = parsed
		}
		if conditions, exists := opMap["conditions"]; exists {
			parsed, err := parseConditionOperations(conditions)
			if err != nil {
				return nil, false
			}
			op.Conditions = parsed
		}

		operations = append(operations, op)
	}

	return operations, true
}

func applyOperations(jsonData []byte, operations []paramOperation, conditionContext map[string]any) ([]byte, error) {
	contextMap := ensureContextMap(conditionContext)
	contextJSON, err := marshalContextJSON(contextMap)
	if err != nil {
		return nil, err
	}

	result := jsonData
	for _, rawOp := range operations {
		op, err := resolveOperationPlaceholders(rawOp, contextMap)
		if err != nil {
			return nil, err
		}
		ok, err := checkConditions(result, contextJSON, op.Conditions, op.Logic)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}

		mode := strings.TrimSpace(op.Mode)
		opPath := processNegativeIndex(result, op.Path)
		var paths []string
		if isPathBasedOperation(mode) {
			paths, err = resolveOperationPaths(result, opPath)
			if err != nil {
				return nil, err
			}
			if len(paths) == 0 {
				continue
			}
		}

		switch mode {
		case "delete":
			for _, path := range paths {
				result, err = deleteValue(result, path)
				if err != nil {
					break
				}
			}
		case "set":
			for _, path := range paths {
				if op.KeepOrigin && gjson.GetBytes(result, path).Exists() {
					continue
				}
				result, err = sjson.SetBytes(result, path, op.Value)
				if err != nil {
					break
				}
			}
		case "move":
			result, err = moveValue(result, processNegativeIndex(result, op.From), processNegativeIndex(result, op.To))
		case "copy":
			if strings.TrimSpace(op.From) == "" || strings.TrimSpace(op.To) == "" {
				return nil, fmt.Errorf("copy from/to is required")
			}
			result, err = copyValue(result, processNegativeIndex(result, op.From), processNegativeIndex(result, op.To))
		case "prepend":
			for _, path := range paths {
				result, err = modifyValue(result, path, op.Value, op.KeepOrigin, true)
				if err != nil {
					break
				}
			}
		case "append":
			for _, path := range paths {
				result, err = modifyValue(result, path, op.Value, op.KeepOrigin, false)
				if err != nil {
					break
				}
			}
		case "array_remove":
			for _, path := range paths {
				result, err = applyBodyArrayRemove(result, path, op.Match)
				if err != nil {
					break
				}
			}
		case "trim_prefix":
			for _, path := range paths {
				result, err = trimStringValue(result, path, op.Value, true)
				if err != nil {
					break
				}
			}
		case "trim_suffix":
			for _, path := range paths {
				result, err = trimStringValue(result, path, op.Value, false)
				if err != nil {
					break
				}
			}
		case "ensure_prefix":
			for _, path := range paths {
				result, err = ensureStringAffix(result, path, op.Value, true)
				if err != nil {
					break
				}
			}
		case "ensure_suffix":
			for _, path := range paths {
				result, err = ensureStringAffix(result, path, op.Value, false)
				if err != nil {
					break
				}
			}
		case "trim_space":
			for _, path := range paths {
				result, err = transformStringValue(result, path, strings.TrimSpace)
				if err != nil {
					break
				}
			}
		case "to_lower":
			for _, path := range paths {
				result, err = transformStringValue(result, path, strings.ToLower)
				if err != nil {
					break
				}
			}
		case "to_upper":
			for _, path := range paths {
				result, err = transformStringValue(result, path, strings.ToUpper)
				if err != nil {
					break
				}
			}
		case "replace":
			for _, path := range paths {
				result, err = replaceStringValue(result, path, op.From, op.To)
				if err != nil {
					break
				}
			}
		case "regex_replace":
			for _, path := range paths {
				result, err = regexReplaceStringValue(result, path, op.From, op.To)
				if err != nil {
					break
				}
			}
		case "return_error":
			return nil, parseParamOverrideReturnError(op.Value)
		case "prune_objects":
			for _, path := range paths {
				result, err = pruneObjects(result, path, contextJSON, op.Value)
				if err != nil {
					break
				}
			}
		case "set_header":
			err = setRuntimeRequestHeaderInContext(contextMap, op.Path, op.Value, op.KeepOrigin)
			if err == nil {
				contextJSON, err = marshalContextJSON(contextMap)
			}
		case "delete_header":
			err = deleteRuntimeRequestHeaderInContext(contextMap, op.Path)
			if err == nil {
				contextJSON, err = marshalContextJSON(contextMap)
			}
		case "copy_header":
			from, to := headerMoveCopyNames(op)
			err = copyHeaderInContext(contextMap, from, to, op.KeepOrigin)
			if errors.Is(err, errSourceHeaderNotFound) {
				err = nil
			}
			if err == nil {
				contextJSON, err = marshalContextJSON(contextMap)
			}
		case "move_header":
			from, to := headerMoveCopyNames(op)
			err = moveHeaderInContext(contextMap, from, to, op.KeepOrigin)
			if errors.Is(err, errSourceHeaderNotFound) {
				err = nil
			}
			if err == nil {
				contextJSON, err = marshalContextJSON(contextMap)
			}
		case "pass_headers":
			names, parseErr := parseHeaderPassThroughNames(op.Value)
			if parseErr != nil {
				return nil, parseErr
			}
			for _, name := range names {
				err = copyHeaderInContext(contextMap, name, name, op.KeepOrigin)
				if errors.Is(err, errSourceHeaderNotFound) {
					err = nil
					continue
				}
				if err != nil {
					break
				}
			}
			if err == nil {
				contextJSON, err = marshalContextJSON(contextMap)
			}
		case "sync_fields":
			result, err = syncFieldsBetweenTargets(result, contextMap, op.From, op.To)
			if err == nil {
				contextJSON, err = marshalContextJSON(contextMap)
			}
		default:
			return nil, fmt.Errorf("unknown operation: %s", op.Mode)
		}

		if err != nil {
			return nil, fmt.Errorf("operation %s failed: %w", op.Mode, err)
		}
	}

	return result, nil
}

func resolveOperationPlaceholders(op paramOperation, contextMap map[string]any) (paramOperation, error) {
	var err error
	if op.Path, err = expandStringPlaceholders(op.Path, contextMap); err != nil {
		return op, err
	}
	if op.From, err = expandStringPlaceholders(op.From, contextMap); err != nil {
		return op, err
	}
	if op.To, err = expandStringPlaceholders(op.To, contextMap); err != nil {
		return op, err
	}
	if op.Value, err = resolveValuePlaceholders(op.Value, contextMap); err != nil {
		return op, err
	}
	for i := range op.Conditions {
		if op.Conditions[i].Path, err = expandStringPlaceholders(op.Conditions[i].Path, contextMap); err != nil {
			return op, err
		}
		if op.Conditions[i].Value, err = resolveValuePlaceholders(op.Conditions[i].Value, contextMap); err != nil {
			return op, err
		}
	}
	if op.Match != nil {
		if op.Match.Path, err = expandStringPlaceholders(op.Match.Path, contextMap); err != nil {
			return op, err
		}
		if op.Match.Eq, err = resolveValuePlaceholders(op.Match.Eq, contextMap); err != nil {
			return op, err
		}
	}
	return op, nil
}

func resolveValuePlaceholders(value any, contextMap map[string]any) (any, error) {
	switch typed := value.(type) {
	case string:
		return expandStringPlaceholders(typed, contextMap)
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			next, err := resolveValuePlaceholders(item, contextMap)
			if err != nil {
				return nil, err
			}
			result[i] = next
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, val := range typed {
			nextKey, err := expandStringPlaceholders(key, contextMap)
			if err != nil {
				return nil, err
			}
			nextVal, err := resolveValuePlaceholders(val, contextMap)
			if err != nil {
				return nil, err
			}
			result[nextKey] = nextVal
		}
		return result, nil
	default:
		return value, nil
	}
}

func expandStringPlaceholders(input string, contextMap map[string]any) (string, error) {
	if input == "" || !strings.Contains(input, "{") {
		return input, nil
	}
	var resolveErr error
	result := placeholderRegexp.ReplaceAllStringFunc(input, func(match string) string {
		if resolveErr != nil {
			return ""
		}
		groups := placeholderRegexp.FindStringSubmatch(match)
		if len(groups) != 2 {
			return match
		}
		value, err := resolvePlaceholder(groups[1], contextMap)
		if err != nil {
			resolveErr = err
			return ""
		}
		return value
	})
	if resolveErr != nil {
		return "", resolveErr
	}
	return result, nil
}

func resolvePlaceholder(token string, contextMap map[string]any) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", nil
	}
	lower := strings.ToLower(token)
	if lower == "api_key" {
		if contextMap == nil {
			return "", nil
		}
		return contextScalarString(contextMap["api_key"])
	}
	for _, prefix := range []string{"client_header:", "request_header:", "header:"} {
		if strings.HasPrefix(lower, prefix) {
			headerName := strings.TrimSpace(token[len(prefix):])
			if headerName == "" {
				return "", fmt.Errorf("%s placeholder name is empty", strings.TrimSuffix(prefix, ":"))
			}
			if prefix == "client_header:" {
				value, _ := getHeaderValueFromContextKey(contextMap, paramOverrideContextRequestHeaders, headerName)
				return value, nil
			}
			value, _ := getHeaderValueFromContext(contextMap, headerName)
			return value, nil
		}
	}
	return contextValueByPath(contextMap, token)
}

func contextValueByPath(contextMap map[string]any, path string) (string, error) {
	if contextMap == nil || strings.TrimSpace(path) == "" {
		return "", nil
	}
	if value, ok := contextMap[path]; ok {
		return contextScalarString(value)
	}
	raw, err := json.Marshal(contextMap)
	if err != nil {
		return "", err
	}
	value := gjson.GetBytes(raw, path)
	if !value.Exists() || value.Type == gjson.Null {
		return "", nil
	}
	return contextScalarString(value.Value())
}

func contextScalarString(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case json.Number:
		return typed.String(), nil
	case map[string]any, []any:
		raw, err := json.Marshal(typed)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed)), nil
	}
}

func headerMoveCopyNames(op paramOperation) (string, string) {
	source := strings.TrimSpace(op.From)
	target := strings.TrimSpace(op.To)
	if source == "" {
		source = strings.TrimSpace(op.Path)
	}
	if target == "" {
		target = strings.TrimSpace(op.Path)
	}
	return source, target
}

func checkConditions(data []byte, contextJSON string, conditions []conditionOperation, logic string) (bool, error) {
	if len(conditions) == 0 {
		return true, nil
	}

	useAnd := strings.EqualFold(strings.TrimSpace(logic), "AND")
	matchedAny := false
	for _, condition := range conditions {
		matched, err := checkSingleCondition(data, contextJSON, condition)
		if err != nil {
			return false, err
		}
		if useAnd && !matched {
			return false, nil
		}
		if matched {
			matchedAny = true
		}
	}

	if useAnd {
		return true, nil
	}
	return matchedAny, nil
}

func checkSingleCondition(data []byte, contextJSON string, condition conditionOperation) (bool, error) {
	path := processNegativeIndex(data, condition.Path)
	value := gjson.GetBytes(data, path)
	if !value.Exists() && contextJSON != "" {
		value = gjson.Get(contextJSON, condition.Path)
	}
	if !value.Exists() {
		return condition.PassMissingKey, nil
	}

	targetBytes, err := json.Marshal(condition.Value)
	if err != nil {
		return false, err
	}

	result, err := compareGjsonValues(value, gjson.ParseBytes(targetBytes), strings.ToLower(condition.Mode))
	if err != nil {
		return false, err
	}
	if condition.Invert {
		result = !result
	}
	return result, nil
}

func compareGjsonValues(jsonValue, targetValue gjson.Result, mode string) (bool, error) {
	switch mode {
	case "full", "":
		return compareEqual(jsonValue, targetValue)
	case "prefix":
		return strings.HasPrefix(jsonValue.String(), targetValue.String()), nil
	case "suffix":
		return strings.HasSuffix(jsonValue.String(), targetValue.String()), nil
	case "contains":
		return strings.Contains(jsonValue.String(), targetValue.String()), nil
	case "gt", "gte", "lt", "lte":
		if jsonValue.Type != gjson.Number || targetValue.Type != gjson.Number {
			return false, fmt.Errorf("numeric comparison requires numbers")
		}
		switch mode {
		case "gt":
			return jsonValue.Num > targetValue.Num, nil
		case "gte":
			return jsonValue.Num >= targetValue.Num, nil
		case "lt":
			return jsonValue.Num < targetValue.Num, nil
		default:
			return jsonValue.Num <= targetValue.Num, nil
		}
	default:
		return false, fmt.Errorf("unsupported comparison mode: %s", mode)
	}
}

func compareEqual(jsonValue, targetValue gjson.Result) (bool, error) {
	if jsonValue.Type == gjson.Null || targetValue.Type == gjson.Null {
		return jsonValue.Type == gjson.Null && targetValue.Type == gjson.Null, nil
	}
	if (jsonValue.Type == gjson.True || jsonValue.Type == gjson.False) &&
		(targetValue.Type == gjson.True || targetValue.Type == gjson.False) {
		return jsonValue.Bool() == targetValue.Bool(), nil
	}
	if jsonValue.Type != targetValue.Type {
		return false, fmt.Errorf("compare for different types")
	}
	switch jsonValue.Type {
	case gjson.Number:
		return jsonValue.Num == targetValue.Num, nil
	case gjson.String:
		return jsonValue.String() == targetValue.String(), nil
	default:
		return jsonValue.String() == targetValue.String(), nil
	}
}

func processNegativeIndex(data []byte, path string) string {
	matches := negativeIndexRegexp.FindAllStringSubmatch(path, -1)
	if len(matches) == 0 {
		return path
	}
	result := path
	for _, match := range matches {
		negIndex := match[1]
		index, _ := strconv.Atoi(negIndex)
		arrayPath := strings.Split(path, negIndex)[0]
		arrayPath = strings.TrimSuffix(arrayPath, ".")
		array := gjson.GetBytes(data, arrayPath)
		if !array.IsArray() {
			continue
		}
		actual := len(array.Array()) + index
		if actual >= 0 && actual < len(array.Array()) {
			result = strings.Replace(result, match[0], "."+strconv.Itoa(actual), 1)
		}
	}
	return result
}

func isPathBasedOperation(mode string) bool {
	switch mode {
	case "delete", "set", "prepend", "append", "array_remove", "trim_prefix", "trim_suffix", "ensure_prefix", "ensure_suffix", "trim_space", "to_lower", "to_upper", "replace", "regex_replace", "prune_objects":
		return true
	default:
		return false
	}
}

func resolveOperationPaths(data []byte, path string) ([]string, error) {
	if !strings.Contains(path, "*") {
		return []string{path}, nil
	}
	return expandWildcardPaths(data, path)
}

func expandWildcardPaths(data []byte, path string) ([]string, error) {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	paths := collectWildcardPaths(root, strings.Split(path, "."), nil)
	return uniqStrings(paths), nil
}

func collectWildcardPaths(node any, segments []string, prefix []string) []string {
	if len(segments) == 0 {
		return []string{strings.Join(prefix, ".")}
	}

	segment := strings.TrimSpace(segments[0])
	if segment == "" {
		return nil
	}
	isLast := len(segments) == 1

	if segment == "*" {
		switch typed := node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			var result []string
			for _, key := range keys {
				result = append(result, collectWildcardPaths(typed[key], segments[1:], append(prefix, key))...)
			}
			return result
		case []any:
			var result []string
			for i, item := range typed {
				result = append(result, collectWildcardPaths(item, segments[1:], append(prefix, strconv.Itoa(i)))...)
			}
			return result
		default:
			return nil
		}
	}

	switch typed := node.(type) {
	case map[string]any:
		if isLast {
			return []string{strings.Join(append(prefix, segment), ".")}
		}
		next, exists := typed[segment]
		if !exists {
			return nil
		}
		return collectWildcardPaths(next, segments[1:], append(prefix, segment))
	case []any:
		index, err := strconv.Atoi(segment)
		if err != nil || index < 0 || index >= len(typed) {
			return nil
		}
		if isLast {
			return []string{strings.Join(append(prefix, segment), ".")}
		}
		return collectWildcardPaths(typed[index], segments[1:], append(prefix, segment))
	default:
		return nil
	}
}

func deleteValue(data []byte, path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return data, nil
	}
	return sjson.DeleteBytes(data, path)
}

func moveValue(data []byte, fromPath, toPath string) ([]byte, error) {
	source := gjson.GetBytes(data, fromPath)
	if !source.Exists() {
		return data, fmt.Errorf("source path does not exist: %s", fromPath)
	}
	result, err := sjson.SetBytes(data, toPath, source.Value())
	if err != nil {
		return nil, err
	}
	return sjson.DeleteBytes(result, fromPath)
}

func copyValue(data []byte, fromPath, toPath string) ([]byte, error) {
	source := gjson.GetBytes(data, fromPath)
	if !source.Exists() {
		return data, fmt.Errorf("source path does not exist: %s", fromPath)
	}
	return sjson.SetBytes(data, toPath, source.Value())
}

func modifyValue(data []byte, path string, value any, keepOrigin bool, prepend bool) ([]byte, error) {
	current := gjson.GetBytes(data, path)
	switch {
	case current.IsArray():
		return modifyArray(data, path, value, prepend)
	case current.Type == gjson.String:
		currentStr := current.String()
		valueStr := fmt.Sprintf("%v", value)
		if prepend {
			return sjson.SetBytes(data, path, valueStr+currentStr)
		}
		return sjson.SetBytes(data, path, currentStr+valueStr)
	case current.Type == gjson.JSON:
		return mergeObjects(data, path, value, keepOrigin)
	default:
		return data, fmt.Errorf("operation not supported for type: %v", current.Type)
	}
}

func modifyArray(data []byte, path string, value any, prepend bool) ([]byte, error) {
	current := gjson.GetBytes(data, path)
	var next []any
	addValue := func() {
		if values, ok := value.([]any); ok {
			next = append(next, values...)
			return
		}
		next = append(next, value)
	}
	addCurrent := func() {
		current.ForEach(func(_, val gjson.Result) bool {
			next = append(next, val.Value())
			return true
		})
	}
	if prepend {
		addValue()
		addCurrent()
	} else {
		addCurrent()
		addValue()
	}
	return sjson.SetBytes(data, path, next)
}

// applyBodyArrayRemove removes items from the array at path whose field at
// match.Path (resolved relative to each item) equals match.Eq. Ported from the
// upstream array_remove override op. A nil match or a non-array target is a
// no-op.
func applyBodyArrayRemove(data []byte, path string, match *paramOverrideMatch) ([]byte, error) {
	current := gjson.GetBytes(data, path)
	if !current.IsArray() || match == nil || strings.TrimSpace(match.Path) == "" {
		return data, nil
	}

	next := make([]any, 0, current.Get("#").Int())
	current.ForEach(func(_, item gjson.Result) bool {
		if !arrayItemMatches(item, match) {
			next = append(next, item.Value())
		}
		return true
	})

	return sjson.SetBytes(data, path, next)
}

// arrayItemMatches reports whether the array item's field at match.Path equals
// match.Eq. Values are compared as strings, mirroring the upstream eq matcher;
// a missing field never matches.
func arrayItemMatches(item gjson.Result, match *paramOverrideMatch) bool {
	if !item.IsObject() {
		return false
	}
	field := item.Get(match.Path)
	if !field.Exists() || field.Type == gjson.Null {
		return false
	}
	return field.Raw == valueToRawEqual(match.Eq)
}

// valueToRawEqual renders a scalar match.Eq the way gjson.Raw presents a JSON
// scalar, so equality comparisons are stable across string/number/bool.
func valueToRawEqual(v any) string {
	switch t := v.(type) {
	case string:
		b, _ := json.Marshal(t)
		return string(b)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func trimStringValue(data []byte, path string, value any, prefix bool) ([]byte, error) {
	current := gjson.GetBytes(data, path)
	if current.Type != gjson.String {
		return data, fmt.Errorf("operation not supported for type: %v", current.Type)
	}
	if value == nil {
		return data, fmt.Errorf("trim value is required")
	}
	valueStr := fmt.Sprintf("%v", value)
	if prefix {
		return sjson.SetBytes(data, path, strings.TrimPrefix(current.String(), valueStr))
	}
	return sjson.SetBytes(data, path, strings.TrimSuffix(current.String(), valueStr))
}

func ensureStringAffix(data []byte, path string, value any, prefix bool) ([]byte, error) {
	current := gjson.GetBytes(data, path)
	if current.Type != gjson.String {
		return data, fmt.Errorf("operation not supported for type: %v", current.Type)
	}
	if value == nil {
		return data, fmt.Errorf("ensure value is required")
	}
	valueStr := fmt.Sprintf("%v", value)
	if valueStr == "" {
		return data, fmt.Errorf("ensure value is required")
	}
	currentStr := current.String()
	if prefix {
		if strings.HasPrefix(currentStr, valueStr) {
			return data, nil
		}
		return sjson.SetBytes(data, path, valueStr+currentStr)
	}
	if strings.HasSuffix(currentStr, valueStr) {
		return data, nil
	}
	return sjson.SetBytes(data, path, currentStr+valueStr)
}

func transformStringValue(data []byte, path string, transform func(string) string) ([]byte, error) {
	current := gjson.GetBytes(data, path)
	if current.Type != gjson.String {
		return data, fmt.Errorf("operation not supported for type: %v", current.Type)
	}
	return sjson.SetBytes(data, path, transform(current.String()))
}

func replaceStringValue(data []byte, path, from, to string) ([]byte, error) {
	current := gjson.GetBytes(data, path)
	if current.Type != gjson.String {
		return data, fmt.Errorf("operation not supported for type: %v", current.Type)
	}
	if from == "" {
		return data, fmt.Errorf("replace from is required")
	}
	return sjson.SetBytes(data, path, strings.ReplaceAll(current.String(), from, to))
}

func regexReplaceStringValue(data []byte, path, pattern, replacement string) ([]byte, error) {
	current := gjson.GetBytes(data, path)
	if current.Type != gjson.String {
		return data, fmt.Errorf("operation not supported for type: %v", current.Type)
	}
	if pattern == "" {
		return data, fmt.Errorf("regex pattern is required")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return data, err
	}
	return sjson.SetBytes(data, path, re.ReplaceAllString(current.String(), replacement))
}

func mergeObjects(data []byte, path string, value any, keepOrigin bool) ([]byte, error) {
	current := gjson.GetBytes(data, path)
	var currentMap map[string]any
	if err := json.Unmarshal([]byte(current.Raw), &currentMap); err != nil {
		return nil, err
	}
	var newMap map[string]any
	switch typed := value.(type) {
	case map[string]any:
		newMap = typed
	default:
		raw, _ := json.Marshal(value)
		if err := json.Unmarshal(raw, &newMap); err != nil {
			return nil, err
		}
	}
	for key, val := range newMap {
		if keepOrigin {
			if _, exists := currentMap[key]; exists {
				continue
			}
		}
		currentMap[key] = val
	}
	return sjson.SetBytes(data, path, currentMap)
}

type pruneObjectsOptions struct {
	conditions []conditionOperation
	logic      string
	recursive  bool
}

func pruneObjects(data []byte, path, contextJSON string, value any) ([]byte, error) {
	options, err := parsePruneObjectsOptions(value)
	if err != nil {
		return nil, err
	}

	if path == "" {
		var root any
		if err := json.Unmarshal(data, &root); err != nil {
			return nil, err
		}
		cleaned, _, err := pruneObjectsNode(root, options, contextJSON, true)
		if err != nil {
			return nil, err
		}
		return json.Marshal(cleaned)
	}

	target := gjson.GetBytes(data, path)
	if !target.Exists() {
		return data, nil
	}

	var node any
	if target.Type == gjson.JSON {
		if err := json.Unmarshal([]byte(target.Raw), &node); err != nil {
			return nil, err
		}
	} else {
		node = target.Value()
	}

	cleaned, _, err := pruneObjectsNode(node, options, contextJSON, true)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(cleaned)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(data, path, raw)
}

func parsePruneObjectsOptions(value any) (pruneObjectsOptions, error) {
	opts := pruneObjectsOptions{logic: "AND", recursive: true}
	switch raw := value.(type) {
	case nil:
		return opts, fmt.Errorf("prune_objects value is required")
	case string:
		if strings.TrimSpace(raw) == "" {
			return opts, fmt.Errorf("prune_objects value is required")
		}
		opts.conditions = []conditionOperation{{Path: "type", Mode: "full", Value: raw}}
	case map[string]any:
		if logic, ok := raw["logic"].(string); ok && strings.TrimSpace(logic) != "" {
			opts.logic = logic
		}
		if recursive, ok := raw["recursive"].(bool); ok {
			opts.recursive = recursive
		}
		if conditions, exists := raw["conditions"]; exists {
			parsed, err := parseConditionOperations(conditions)
			if err != nil {
				return opts, err
			}
			opts.conditions = append(opts.conditions, parsed...)
		}
		if whereRaw, exists := raw["where"]; exists {
			where, ok := whereRaw.(map[string]any)
			if !ok {
				return opts, fmt.Errorf("prune_objects where must be object")
			}
			for key, val := range where {
				if strings.TrimSpace(key) != "" {
					opts.conditions = append(opts.conditions, conditionOperation{Path: key, Mode: "full", Value: val})
				}
			}
		}
		if matchType, exists := raw["type"]; exists {
			opts.conditions = append(opts.conditions, conditionOperation{Path: "type", Mode: "full", Value: matchType})
		}
	default:
		return opts, fmt.Errorf("prune_objects value must be string or object")
	}
	if len(opts.conditions) == 0 {
		return opts, fmt.Errorf("prune_objects conditions are required")
	}
	return opts, nil
}

func parseConditionOperations(raw any) ([]conditionOperation, error) {
	switch typed := raw.(type) {
	case map[string]any:
		var result []conditionOperation
		for key, value := range typed {
			if strings.TrimSpace(key) != "" {
				result = append(result, conditionOperation{Path: key, Mode: "full", Value: value})
			}
		}
		if len(result) == 0 {
			return nil, fmt.Errorf("conditions object must contain at least one key")
		}
		return result, nil
	case []any:
		result := make([]conditionOperation, 0, len(typed))
		for _, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("condition must be object")
			}
			path, _ := itemMap["path"].(string)
			mode, _ := itemMap["mode"].(string)
			if strings.TrimSpace(path) == "" || strings.TrimSpace(mode) == "" {
				return nil, fmt.Errorf("condition path/mode is required")
			}
			condition := conditionOperation{Path: path, Mode: mode}
			if value, exists := itemMap["value"]; exists {
				condition.Value = value
			}
			if invert, ok := itemMap["invert"].(bool); ok {
				condition.Invert = invert
			}
			if passMissingKey, ok := itemMap["pass_missing_key"].(bool); ok {
				condition.PassMissingKey = passMissingKey
			}
			result = append(result, condition)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("conditions must be an array or object")
	}
}

func pruneObjectsNode(node any, options pruneObjectsOptions, contextJSON string, root bool) (any, bool, error) {
	switch value := node.(type) {
	case []any:
		result := make([]any, 0, len(value))
		for _, item := range value {
			next, drop, err := pruneObjectsNode(item, options, contextJSON, false)
			if err != nil {
				return nil, false, err
			}
			if !drop {
				result = append(result, next)
			}
		}
		return result, false, nil
	case map[string]any:
		shouldDrop, err := shouldPruneObject(value, options, contextJSON)
		if err != nil {
			return nil, false, err
		}
		if shouldDrop && !root {
			return nil, true, nil
		}
		if !options.recursive {
			return value, false, nil
		}
		for key, child := range value {
			next, drop, err := pruneObjectsNode(child, options, contextJSON, false)
			if err != nil {
				return nil, false, err
			}
			if drop {
				delete(value, key)
				continue
			}
			value[key] = next
		}
		return value, false, nil
	default:
		return node, false, nil
	}
}

func shouldPruneObject(node map[string]any, options pruneObjectsOptions, contextJSON string) (bool, error) {
	raw, err := json.Marshal(node)
	if err != nil {
		return false, err
	}
	return checkConditions(raw, contextJSON, options.conditions, options.logic)
}

func parseParamOverrideReturnError(value any) error {
	result := &paramOverrideReturnError{
		StatusCode: http.StatusBadRequest,
		Code:       "invalid_request",
		Type:       "invalid_request_error",
		SkipRetry:  true,
	}

	switch raw := value.(type) {
	case nil:
		return fmt.Errorf("return_error value is required")
	case string:
		result.Message = strings.TrimSpace(raw)
	case map[string]any:
		if message, ok := raw["message"].(string); ok {
			result.Message = strings.TrimSpace(message)
		}
		if result.Message == "" {
			if message, ok := raw["msg"].(string); ok {
				result.Message = strings.TrimSpace(message)
			}
		}
		if code, exists := raw["code"]; exists {
			if codeStr := strings.TrimSpace(fmt.Sprintf("%v", code)); codeStr != "" {
				result.Code = codeStr
			}
		}
		if errType, ok := raw["type"].(string); ok && strings.TrimSpace(errType) != "" {
			result.Type = strings.TrimSpace(errType)
		}
		if skipRetry, ok := raw["skip_retry"].(bool); ok {
			result.SkipRetry = skipRetry
		}
		if statusRaw, exists := raw["status_code"]; exists {
			statusCode, ok := parseOverrideInt(statusRaw)
			if !ok {
				return fmt.Errorf("return_error status_code must be an integer")
			}
			result.StatusCode = statusCode
		} else if statusRaw, exists := raw["status"]; exists {
			statusCode, ok := parseOverrideInt(statusRaw)
			if !ok {
				return fmt.Errorf("return_error status must be an integer")
			}
			result.StatusCode = statusCode
		}
	default:
		return fmt.Errorf("return_error value must be string or object")
	}

	if result.Message == "" {
		return fmt.Errorf("return_error message is required")
	}
	if result.StatusCode < http.StatusContinue || result.StatusCode > http.StatusNetworkAuthenticationRequired {
		return fmt.Errorf("return_error status code out of range: %d", result.StatusCode)
	}
	return result
}

func parseOverrideInt(v any) (int, bool) {
	switch value := v.(type) {
	case int:
		return value, true
	case float64:
		if value != float64(int(value)) {
			return 0, false
		}
		return int(value), true
	default:
		return 0, false
	}
}

func buildParamOverrideContext(requestCtx context.Context, outbound *PersistentOutboundTransformer, request *httpclient.Request) map[string]any {
	contextMap := make(map[string]any)
	if outbound == nil || outbound.state == nil {
		return contextMap
	}

	if outbound.state.CurrentCandidate != nil && len(outbound.state.CurrentCandidate.Models) > outbound.state.CurrentModelIndex {
		model := outbound.state.CurrentCandidate.Models[outbound.state.CurrentModelIndex].ActualModel
		contextMap["model"] = model
		contextMap["upstream_model"] = model
	}
	if outbound.state.OriginalModel != "" {
		contextMap["original_model"] = outbound.state.OriginalModel
		if _, exists := contextMap["model"]; !exists {
			contextMap["model"] = outbound.state.OriginalModel
		}
	}
	contextMap["api_key"] = currentAPIKey(requestCtx, outbound, request)
	if request != nil && request.Path != "" {
		contextMap["request_path"] = request.Path
	}

	contextMap[paramOverrideContextRequestHeaders] = buildRequestHeadersContext(outbound.state.LlmRequest)
	contextMap[paramOverrideContextRuntimeRequestHeaders] = map[string]any{}
	contextMap["retry_index"] = outbound.state.RetryIndex
	contextMap["is_retry"] = outbound.state.RetryIndex > 0
	contextMap["retry"] = map[string]any{"index": outbound.state.RetryIndex, "is_retry": outbound.state.RetryIndex > 0}

	if outbound.state.LastError != nil {
		contextMap["last_error"] = map[string]any{
			"status_code": outbound.state.LastError.StatusCode,
			"message":     string(outbound.state.LastError.Body),
		}
		contextMap["last_error_status_code"] = outbound.state.LastError.StatusCode
		contextMap["last_error_message"] = string(outbound.state.LastError.Body)
	}

	return contextMap
}

func buildRequestHeadersContext(llmReq *llm.Request) map[string]any {
	result := make(map[string]any)
	if llmReq == nil || llmReq.RawRequest == nil || llmReq.RawRequest.Headers == nil {
		return result
	}

	for key, values := range llmReq.RawRequest.Headers {
		if len(values) == 0 {
			continue
		}
		normalized := normalizeHeaderContextKey(key)
		value := strings.TrimSpace(values[0])
		if normalized == "" || value == "" {
			continue
		}
		result[normalized] = value
	}
	return result
}

func ensureContextMap(conditionContext map[string]any) map[string]any {
	if conditionContext != nil {
		return conditionContext
	}
	return make(map[string]any)
}

func marshalContextJSON(context map[string]any) (string, error) {
	if len(context) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(context)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func applyRuntimeRequestHeadersFromContext(request *httpclient.Request, contextMap map[string]any) {
	if request == nil || contextMap == nil {
		return
	}
	raw, exists := contextMap[paramOverrideContextRuntimeRequestHeaders]
	if !exists {
		return
	}
	rawMap, ok := raw.(map[string]any)
	if !ok {
		return
	}
	request.TransformerMetadata = ensureTransformerMetadata(request.TransformerMetadata)
	request.TransformerMetadata["runtime_request_headers"] = sanitizeRuntimeRequestHeaderMap(rawMap)
}

func ensureTransformerMetadata(metadata map[string]any) map[string]any {
	if metadata != nil {
		return metadata
	}
	return make(map[string]any)
}

func setRuntimeRequestHeaderInContext(contextMap map[string]any, headerName string, value any, keepOrigin bool) error {
	headerName = normalizeHeaderContextKey(headerName)
	if headerName == "" {
		return fmt.Errorf("header name is required")
	}
	headers := ensureMapKeyInContext(contextMap, paramOverrideContextRuntimeRequestHeaders)
	if keepOrigin {
		if existing, ok := headers[headerName]; ok && strings.TrimSpace(fmt.Sprintf("%v", existing)) != "" {
			return nil
		}
	}
	headerValue, hasValue, err := resolveRuntimeRequestHeaderValue(contextMap, headerName, value)
	if err != nil {
		return err
	}
	if !hasValue {
		delete(headers, headerName)
		return nil
	}
	headers[headerName] = headerValue
	return nil
}

func resolveRuntimeRequestHeaderValue(contextMap map[string]any, headerName string, value any) (string, bool, error) {
	if value == nil {
		return "", false, fmt.Errorf("header value is required")
	}
	if mapping, ok := value.(map[string]any); ok {
		return resolveRuntimeRequestHeaderValueByMapping(contextMap, headerName, mapping)
	}
	headerValue := strings.TrimSpace(fmt.Sprintf("%v", value))
	if headerValue == "" {
		return "", false, nil
	}
	return headerValue, true, nil
}

func resolveRuntimeRequestHeaderValueByMapping(contextMap map[string]any, headerName string, mapping map[string]any) (string, bool, error) {
	appendTokens, err := parseHeaderAppendTokens(mapping)
	if err != nil {
		return "", false, err
	}
	keepOnlyDeclared := false
	if raw, ok := mapping["$keep_only_declared"].(bool); ok {
		keepOnlyDeclared = raw
	}

	sourceValue, exists := getHeaderValueFromContext(contextMap, headerName)
	sourceTokens := []string{}
	if exists {
		sourceTokens = splitHeaderListValue(sourceValue)
	}

	wildcardValue, hasWildcard := mapping["*"]
	resultTokens := make([]string, 0, len(sourceTokens)+len(appendTokens))
	for _, token := range sourceTokens {
		replacement, hasReplacement := mapping[token]
		if !hasReplacement && hasWildcard && !keepOnlyDeclared {
			replacement = wildcardValue
			hasReplacement = true
		}
		if !hasReplacement {
			if !keepOnlyDeclared {
				resultTokens = append(resultTokens, token)
			}
			continue
		}
		replacementTokens, err := parseHeaderReplacementTokens(replacement)
		if err != nil {
			return "", false, err
		}
		resultTokens = append(resultTokens, replacementTokens...)
	}

	resultTokens = append(resultTokens, appendTokens...)
	resultTokens = uniqStrings(resultTokens)
	if len(resultTokens) == 0 {
		return "", false, nil
	}
	return strings.Join(resultTokens, ","), true, nil
}

func parseHeaderAppendTokens(mapping map[string]any) ([]string, error) {
	appendRaw, ok := mapping["$append"]
	if !ok {
		return nil, nil
	}
	return parseHeaderReplacementTokens(appendRaw)
}

func parseHeaderReplacementTokens(value any) ([]string, error) {
	switch raw := value.(type) {
	case nil:
		return nil, nil
	case string:
		return splitHeaderListValue(raw), nil
	case []any:
		var result []string
		for _, item := range raw {
			items, err := parseHeaderReplacementTokens(item)
			if err != nil {
				return nil, err
			}
			result = append(result, items...)
		}
		return uniqStrings(result), nil
	default:
		token := strings.TrimSpace(fmt.Sprintf("%v", raw))
		if token == "" {
			return nil, nil
		}
		return []string{token}, nil
	}
}

func splitHeaderListValue(raw string) []string {
	parts := strings.Split(raw, ",")
	var result []string
	for _, item := range parts {
		token := strings.TrimSpace(item)
		if token != "" {
			result = append(result, token)
		}
	}
	return result
}

func copyHeaderInContext(contextMap map[string]any, fromHeader, toHeader string, keepOrigin bool) error {
	fromHeader = normalizeHeaderContextKey(fromHeader)
	toHeader = normalizeHeaderContextKey(toHeader)
	if fromHeader == "" || toHeader == "" {
		return fmt.Errorf("copy_header from/to is required")
	}
	value, exists := getHeaderValueFromContext(contextMap, fromHeader)
	if !exists {
		return fmt.Errorf("%w: %s", errSourceHeaderNotFound, fromHeader)
	}
	return setRuntimeRequestHeaderInContext(contextMap, toHeader, value, keepOrigin)
}

func moveHeaderInContext(contextMap map[string]any, fromHeader, toHeader string, keepOrigin bool) error {
	fromHeader = normalizeHeaderContextKey(fromHeader)
	toHeader = normalizeHeaderContextKey(toHeader)
	if fromHeader == "" || toHeader == "" {
		return fmt.Errorf("move_header from/to is required")
	}
	if err := copyHeaderInContext(contextMap, fromHeader, toHeader, keepOrigin); err != nil {
		return err
	}
	if strings.EqualFold(fromHeader, toHeader) {
		return nil
	}
	return deleteRuntimeRequestHeaderInContext(contextMap, fromHeader)
}

func deleteRuntimeRequestHeaderInContext(contextMap map[string]any, headerName string) error {
	headerName = normalizeHeaderContextKey(headerName)
	if headerName == "" {
		return fmt.Errorf("header name is required")
	}
	headers := ensureMapKeyInContext(contextMap, paramOverrideContextRuntimeRequestHeaders)
	delete(headers, headerName)
	return nil
}

func parseHeaderPassThroughNames(value any) ([]string, error) {
	normalize := func(values []string) []string {
		var result []string
		for _, item := range values {
			name := normalizeHeaderContextKey(item)
			if name != "" {
				result = append(result, name)
			}
		}
		return uniqStrings(result)
	}

	switch raw := value.(type) {
	case nil:
		return nil, fmt.Errorf("pass_headers value is required")
	case string:
		if strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("pass_headers value is required")
		}
		return normalize(strings.Split(raw, ",")), nil
	case []any:
		values := make([]string, 0, len(raw))
		for _, item := range raw {
			values = append(values, fmt.Sprintf("%v", item))
		}
		return normalize(values), nil
	case map[string]any:
		var result []string
		for _, key := range []string{"headers", "names", "header"} {
			if namesRaw, ok := raw[key]; ok {
				names, err := parseHeaderPassThroughNames(namesRaw)
				if err == nil {
					result = append(result, names...)
				}
			}
		}
		result = uniqStrings(result)
		if len(result) == 0 {
			return nil, fmt.Errorf("pass_headers value is invalid")
		}
		return result, nil
	default:
		return nil, fmt.Errorf("pass_headers value must be string, array or object")
	}
}

func ensureMapKeyInContext(contextMap map[string]any, key string) map[string]any {
	if existing, ok := contextMap[key]; ok {
		if mapVal, ok := existing.(map[string]any); ok {
			return mapVal
		}
	}
	result := make(map[string]any)
	contextMap[key] = result
	return result
}

func getHeaderValueFromContext(contextMap map[string]any, headerName string) (string, bool) {
	headerName = normalizeHeaderContextKey(headerName)
	if headerName == "" {
		return "", false
	}
	for _, key := range []string{paramOverrideContextRuntimeRequestHeaders, paramOverrideContextRequestHeaders} {
		if value, ok := getHeaderValueFromContextKey(contextMap, key, headerName); ok {
			return value, true
		}
	}
	return "", false
}

func getHeaderValueFromContextKey(contextMap map[string]any, contextKey string, headerName string) (string, bool) {
	headerName = normalizeHeaderContextKey(headerName)
	if contextMap == nil || headerName == "" {
		return "", false
	}
	source, ok := contextMap[contextKey].(map[string]any)
	if !ok {
		return "", false
	}
	raw, ok := source[headerName]
	if !ok {
		return "", false
	}
	value := strings.TrimSpace(fmt.Sprintf("%v", raw))
	return value, value != ""
}

type syncTarget struct {
	kind string
	key  string
}

func parseSyncTarget(spec string) (syncTarget, error) {
	raw := strings.TrimSpace(spec)
	if raw == "" {
		return syncTarget{}, fmt.Errorf("sync_fields target is required")
	}
	idx := strings.Index(raw, ":")
	if idx < 0 {
		return syncTarget{kind: "json", key: raw}, nil
	}
	kind := strings.ToLower(strings.TrimSpace(raw[:idx]))
	key := strings.TrimSpace(raw[idx+1:])
	if key == "" {
		return syncTarget{}, fmt.Errorf("sync_fields target key is required")
	}
	switch kind {
	case "json", "body":
		return syncTarget{kind: "json", key: key}, nil
	case "header":
		return syncTarget{kind: "header", key: key}, nil
	default:
		return syncTarget{}, fmt.Errorf("sync_fields target prefix is invalid: %s", raw)
	}
}

func syncFieldsBetweenTargets(data []byte, contextMap map[string]any, fromSpec, toSpec string) ([]byte, error) {
	fromTarget, err := parseSyncTarget(fromSpec)
	if err != nil {
		return nil, err
	}
	toTarget, err := parseSyncTarget(toSpec)
	if err != nil {
		return nil, err
	}
	fromValue, fromExists, err := readSyncTargetValue(data, contextMap, fromTarget)
	if err != nil {
		return nil, err
	}
	toValue, toExists, err := readSyncTargetValue(data, contextMap, toTarget)
	if err != nil {
		return nil, err
	}
	if fromExists && !toExists {
		return writeSyncTargetValue(data, contextMap, toTarget, fromValue)
	}
	if toExists && !fromExists {
		return writeSyncTargetValue(data, contextMap, fromTarget, toValue)
	}
	return data, nil
}

func readSyncTargetValue(data []byte, contextMap map[string]any, target syncTarget) (any, bool, error) {
	switch target.kind {
	case "json":
		value := gjson.GetBytes(data, processNegativeIndex(data, target.key))
		if !value.Exists() || value.Type == gjson.Null || (value.Type == gjson.String && strings.TrimSpace(value.String()) == "") {
			return nil, false, nil
		}
		return value.Value(), true, nil
	case "header":
		value, ok := getHeaderValueFromContext(contextMap, target.key)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, false, nil
		}
		return value, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported sync_fields target kind: %s", target.kind)
	}
}

func writeSyncTargetValue(data []byte, contextMap map[string]any, target syncTarget, value any) ([]byte, error) {
	switch target.kind {
	case "json":
		return sjson.SetBytes(data, processNegativeIndex(data, target.key), value)
	case "header":
		return data, setRuntimeRequestHeaderInContext(contextMap, target.key, value, false)
	default:
		return nil, fmt.Errorf("unsupported sync_fields target kind: %s", target.kind)
	}
}

func resolveFinalRequestHeaders(outbound *PersistentOutboundTransformer, source map[string]any) (map[string]string, error) {
	result := make(map[string]string)
	source = sanitizeRuntimeRequestHeaderMap(source)
	passAll := false
	var regexes []*regexp.Regexp

	for key := range source {
		normalized := strings.TrimSpace(strings.ToLower(key))
		if normalized == headerPassthroughAllKey {
			passAll = true
			continue
		}
		var pattern string
		switch {
		case strings.HasPrefix(normalized, headerPassthroughRegexPrefix):
			pattern = strings.TrimSpace(normalized[len(headerPassthroughRegexPrefix):])
		case strings.HasPrefix(normalized, headerPassthroughRegexPrefixV2):
			pattern = strings.TrimSpace(normalized[len(headerPassthroughRegexPrefixV2):])
		}
		if pattern != "" {
			re, err := getHeaderPassthroughRegex(pattern)
			if err != nil {
				return nil, err
			}
			regexes = append(regexes, re)
		}
	}

	if passAll || len(regexes) > 0 {
		headers := inboundHeaders(outbound)
		for name := range headers {
			if shouldSkipPassthroughHeader(name) {
				continue
			}
			if !passAll {
				matched := false
				for _, re := range regexes {
					if re.MatchString(name) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			value := strings.TrimSpace(headers.Get(name))
			if value != "" {
				result[strings.ToLower(strings.TrimSpace(name))] = value
			}
		}
	}

	for key, value := range source {
		if isHeaderPassthroughRuleKey(key) {
			continue
		}
		headerName := strings.TrimSpace(strings.ToLower(key))
		if headerName == "" {
			continue
		}
		str, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("request header value for %q must be string", key)
		}
		if strings.TrimSpace(str) != "" {
			result[headerName] = str
		}
	}

	return result, nil
}

func inboundHeaders(outbound *PersistentOutboundTransformer) http.Header {
	if outbound == nil || outbound.state == nil || outbound.state.LlmRequest == nil || outbound.state.LlmRequest.RawRequest == nil {
		return make(http.Header)
	}
	return outbound.state.LlmRequest.RawRequest.Headers
}

func currentAPIKey(ctx context.Context, outbound *PersistentOutboundTransformer, request *httpclient.Request) string {
	if apiKey, ok := contexts.GetChannelAPIKey(ctx); ok && strings.TrimSpace(apiKey) != "" {
		return apiKey
	}
	if request != nil && request.Auth != nil && strings.TrimSpace(request.Auth.APIKey) != "" {
		return request.Auth.APIKey
	}
	if outbound == nil || outbound.state == nil || outbound.state.CurrentCandidate == nil || outbound.state.CurrentCandidate.Channel == nil {
		return ""
	}
	keys := outbound.state.CurrentCandidate.Channel.GetEnabledAPIKeys()
	if len(keys) > 0 {
		return keys[0]
	}
	return outbound.state.CurrentCandidate.Channel.Credentials.APIKey
}

func sanitizeRuntimeRequestHeaderMap(source map[string]any) map[string]any {
	result := make(map[string]any)
	for key, value := range source {
		normalized := normalizeHeaderContextKey(key)
		if normalized == "" {
			continue
		}
		valueStr := strings.TrimSpace(fmt.Sprintf("%v", value))
		if valueStr == "" {
			if isHeaderPassthroughRuleKey(normalized) {
				result[normalized] = ""
			}
			continue
		}
		result[normalized] = valueStr
	}
	return result
}

func normalizeHeaderContextKey(key string) string {
	return strings.TrimSpace(strings.ToLower(key))
}

func isHeaderPassthroughRuleKey(key string) bool {
	key = strings.TrimSpace(strings.ToLower(key))
	return key == headerPassthroughAllKey || strings.HasPrefix(key, headerPassthroughRegexPrefix) || strings.HasPrefix(key, headerPassthroughRegexPrefixV2)
}

func shouldSkipPassthroughHeader(name string) bool {
	_, ok := passthroughSkipHeaderNamesLower[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func getHeaderPassthroughRegex(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, errors.New("empty regex pattern")
	}
	if cached, ok := headerPassthroughRegexMap.Load(pattern); ok {
		if re, ok := cached.(*regexp.Regexp); ok {
			return re, nil
		}
		headerPassthroughRegexMap.Delete(pattern)
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	actual, _ := headerPassthroughRegexMap.LoadOrStore(pattern, compiled)
	if re, ok := actual.(*regexp.Regexp); ok {
		return re, nil
	}
	return compiled, nil
}

func uniqStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func logParamOverrideError(ctx context.Context, message string, err error) {
	if err != nil {
		log.Warn(ctx, message, log.Cause(err))
	}
}
