package biz

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var supportedParamOverrideOperationModes = map[string]struct{}{
	"delete":        {},
	"set":           {},
	"move":          {},
	"copy":          {},
	"prepend":       {},
	"append":        {},
	"trim_prefix":   {},
	"trim_suffix":   {},
	"ensure_prefix": {},
	"ensure_suffix": {},
	"trim_space":    {},
	"to_lower":      {},
	"to_upper":      {},
	"replace":       {},
	"regex_replace": {},
	"return_error":  {},
	"prune_objects": {},
	"set_header":    {},
	"delete_header": {},
	"copy_header":   {},
	"move_header":   {},
	"pass_headers":  {},
	"sync_fields":   {},
	"array_remove":  {},
}

// GetParamOverrideMap returns the parsed new-api compatible parameter override map.
func (c *Channel) GetParamOverrideMap() map[string]any {
	if c == nil || c.Settings == nil {
		return nil
	}

	result, err := parseOverrideJSONObject(c.Settings.ParamOverride)
	if err != nil {
		return nil
	}

	return result
}

func ValidateParamOverrideJSON(input string) error {
	obj, err := parseOverrideJSONObject(input)
	if err != nil {
		return err
	}
	if len(obj) == 0 {
		return nil
	}
	operations, exists := obj["operations"]
	if !exists {
		return fmt.Errorf("param override operations are required")
	}
	items, ok := operations.([]any)
	if !ok {
		return fmt.Errorf("param override operations must be an array")
	}
	for index, item := range items {
		op, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("param override operation %d must be an object", index+1)
		}
		mode, ok := op["mode"].(string)
		if !ok || strings.TrimSpace(mode) == "" {
			return fmt.Errorf("param override operation %d mode is required", index+1)
		}
		mode = strings.TrimSpace(mode)
		if _, ok := supportedParamOverrideOperationModes[mode]; !ok {
			return fmt.Errorf("param override operation %d mode is unsupported: %s", index+1, mode)
		}
		if err := validateParamOverrideOperation(index+1, mode, op); err != nil {
			return err
		}
	}
	return nil
}

func validateParamOverrideOperation(index int, mode string, op map[string]any) error {
	if err := validateParamOverrideLogic(index, op); err != nil {
		return err
	}
	if conditions, exists := op["conditions"]; exists {
		if err := validateParamOverrideConditions(index, conditions); err != nil {
			return err
		}
	}

	switch mode {
	case "delete", "trim_space", "to_lower", "to_upper":
		return requireStringField(index, mode, op, "path")
	case "set", "prepend", "append", "trim_prefix", "trim_suffix", "ensure_prefix", "ensure_suffix":
		if err := requireStringField(index, mode, op, "path"); err != nil {
			return err
		}
		return requireValueField(index, mode, op, "value")
	case "replace":
		if err := requireStringField(index, mode, op, "path"); err != nil {
			return err
		}
		return requireStringField(index, mode, op, "from")
	case "regex_replace":
		if err := requireStringField(index, mode, op, "path"); err != nil {
			return err
		}
		if err := requireStringField(index, mode, op, "from"); err != nil {
			return err
		}
		pattern, _ := op["from"].(string)
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("param override operation %d regex_replace from is invalid: %w", index, err)
		}
		return nil
	case "move", "copy":
		if err := requireStringField(index, mode, op, "from"); err != nil {
			return err
		}
		return requireStringField(index, mode, op, "to")
	case "return_error":
		return validateParamOverrideReturnErrorValue(index, op["value"])
	case "prune_objects":
		return validateParamOverridePruneObjectsValue(index, op["value"])
	case "set_header":
		if err := requireStringField(index, mode, op, "path"); err != nil {
			return err
		}
		return requireValueField(index, mode, op, "value")
	case "delete_header":
		return requireStringField(index, mode, op, "path")
	case "copy_header", "move_header":
		if strings.TrimSpace(stringField(op, "from")) == "" && strings.TrimSpace(stringField(op, "path")) == "" {
			return fmt.Errorf("param override operation %d %s from/path is required", index, mode)
		}
		if strings.TrimSpace(stringField(op, "to")) == "" && strings.TrimSpace(stringField(op, "path")) == "" {
			return fmt.Errorf("param override operation %d %s to/path is required", index, mode)
		}
		return nil
	case "pass_headers":
		return validateParamOverridePassHeadersValue(index, op["value"])
	case "array_remove":
		// array_remove filters items out of the array at `path` by a match rule:
		// each item is removed when the field at match.path (resolved relative to
		// the item) equals match.eq. Ported from upstream's array_remove override op.
		if err := requireStringField(index, mode, op, "path"); err != nil {
			return err
		}
		match, ok := op["match"].(map[string]any)
		if !ok {
			return fmt.Errorf("param override operation %d array_remove match object is required", index)
		}
		if strings.TrimSpace(stringField(match, "path")) == "" {
			return fmt.Errorf("param override operation %d array_remove match.path is required", index)
		}
		if !matchHasEq(match) {
			return fmt.Errorf("param override operation %d array_remove match.eq is required", index)
		}
		return nil
	case "sync_fields":
		if err := requireStringField(index, mode, op, "from"); err != nil {
			return err
		}
		if err := requireStringField(index, mode, op, "to"); err != nil {
			return err
		}
		if err := validateParamOverrideSyncTarget(index, mode, stringField(op, "from")); err != nil {
			return err
		}
		return validateParamOverrideSyncTarget(index, mode, stringField(op, "to"))
	default:
		return nil
	}
}

func requireStringField(index int, mode string, op map[string]any, field string) error {
	value, ok := op[field].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fmt.Errorf("param override operation %d %s %s is required", index, mode, field)
	}
	return nil
}

func requireValueField(index int, mode string, op map[string]any, field string) error {
	if value, exists := op[field]; !exists || value == nil {
		return fmt.Errorf("param override operation %d %s %s is required", index, mode, field)
	}
	return nil
}

func stringField(op map[string]any, field string) string {
	value, _ := op[field].(string)
	return value
}

// matchHasEq reports whether the array_remove match rule carries a non-nil eq value.
// eq may be any scalar (string/number/bool); nil is invalid.
func matchHasEq(match map[string]any) bool {
	eq, exists := match["eq"]
	if !exists || eq == nil {
		return false
	}
	switch eq.(type) {
	case string, float64, bool:
		return true
	}
	return false
}

func validateParamOverrideLogic(index int, op map[string]any) error {
	logic, ok := op["logic"].(string)
	if !ok || strings.TrimSpace(logic) == "" {
		return nil
	}
	switch strings.ToUpper(strings.TrimSpace(logic)) {
	case "AND", "OR":
		return nil
	default:
		return fmt.Errorf("param override operation %d logic is unsupported: %s", index, logic)
	}
}

func validateParamOverrideConditions(index int, raw any) error {
	switch typed := raw.(type) {
	case map[string]any:
		for key := range typed {
			if strings.TrimSpace(key) != "" {
				return nil
			}
		}
		return fmt.Errorf("param override operation %d conditions object must contain at least one key", index)
	case []any:
		for conditionIndex, item := range typed {
			itemMap, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("param override operation %d condition %d must be an object", index, conditionIndex+1)
			}
			path, _ := itemMap["path"].(string)
			mode, _ := itemMap["mode"].(string)
			if strings.TrimSpace(path) == "" || strings.TrimSpace(mode) == "" {
				return fmt.Errorf("param override operation %d condition %d path/mode is required", index, conditionIndex+1)
			}
			if err := validateParamOverrideConditionMode(index, conditionIndex+1, mode); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("param override operation %d conditions must be an array or object", index)
	}
}

func validateParamOverrideConditionMode(index, conditionIndex int, mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "full", "prefix", "suffix", "contains", "gt", "gte", "lt", "lte":
		return nil
	default:
		return fmt.Errorf("param override operation %d condition %d mode is unsupported: %s", index, conditionIndex, mode)
	}
}

func validateParamOverrideReturnErrorValue(index int, value any) error {
	switch raw := value.(type) {
	case nil:
		return fmt.Errorf("param override operation %d return_error value is required", index)
	case string:
		if strings.TrimSpace(raw) == "" {
			return fmt.Errorf("param override operation %d return_error message is required", index)
		}
		return nil
	case map[string]any:
		message, _ := raw["message"].(string)
		if strings.TrimSpace(message) == "" {
			message, _ = raw["msg"].(string)
		}
		if strings.TrimSpace(message) == "" {
			return fmt.Errorf("param override operation %d return_error message is required", index)
		}
		if skipRetry, exists := raw["skip_retry"]; exists {
			if _, ok := skipRetry.(bool); !ok {
				return fmt.Errorf("param override operation %d return_error skip_retry must be a boolean", index)
			}
		}
		for _, key := range []string{"status_code", "status"} {
			if statusRaw, exists := raw[key]; exists {
				statusCode, ok := parseOverrideJSONInt(statusRaw)
				if !ok {
					return fmt.Errorf("param override operation %d return_error %s must be an integer", index, key)
				}
				if statusCode < http.StatusContinue || statusCode > http.StatusNetworkAuthenticationRequired {
					return fmt.Errorf("param override operation %d return_error status code out of range: %d", index, statusCode)
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("param override operation %d return_error value must be string or object", index)
	}
}

func parseOverrideJSONInt(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	case json.Number:
		n, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func validateParamOverridePruneObjectsValue(index int, value any) error {
	switch raw := value.(type) {
	case nil:
		return fmt.Errorf("param override operation %d prune_objects value is required", index)
	case string:
		if strings.TrimSpace(raw) == "" {
			return fmt.Errorf("param override operation %d prune_objects value is required", index)
		}
		return nil
	case map[string]any:
		conditionCount := 0
		if conditions, exists := raw["conditions"]; exists {
			if err := validateParamOverrideConditions(index, conditions); err != nil {
				return err
			}
			conditionCount++
		}
		if whereRaw, exists := raw["where"]; exists {
			where, ok := whereRaw.(map[string]any)
			if !ok {
				return fmt.Errorf("param override operation %d prune_objects where must be object", index)
			}
			for key := range where {
				if strings.TrimSpace(key) != "" {
					conditionCount++
					break
				}
			}
		}
		if _, exists := raw["type"]; exists {
			conditionCount++
		}
		if conditionCount == 0 {
			return fmt.Errorf("param override operation %d prune_objects conditions are required", index)
		}
		return nil
	default:
		return fmt.Errorf("param override operation %d prune_objects value must be string or object", index)
	}
}

func validateParamOverridePassHeadersValue(index int, value any) error {
	switch raw := value.(type) {
	case nil:
		return fmt.Errorf("param override operation %d pass_headers value is required", index)
	case string:
		if strings.TrimSpace(raw) == "" {
			return fmt.Errorf("param override operation %d pass_headers value is required", index)
		}
		return nil
	case []any:
		for _, item := range raw {
			if strings.TrimSpace(fmt.Sprintf("%v", item)) != "" {
				return nil
			}
		}
		return fmt.Errorf("param override operation %d pass_headers value is required", index)
	case map[string]any:
		for _, key := range []string{"headers", "names", "header"} {
			if namesRaw, ok := raw[key]; ok {
				if err := validateParamOverridePassHeadersValue(index, namesRaw); err == nil {
					return nil
				}
			}
		}
		return fmt.Errorf("param override operation %d pass_headers value is invalid", index)
	default:
		return fmt.Errorf("param override operation %d pass_headers value must be string, array or object", index)
	}
}

func validateParamOverrideSyncTarget(index int, mode, spec string) error {
	raw := strings.TrimSpace(spec)
	if raw == "" {
		return fmt.Errorf("param override operation %d %s target is required", index, mode)
	}
	colon := strings.Index(raw, ":")
	if colon < 0 {
		return nil
	}
	kind := strings.ToLower(strings.TrimSpace(raw[:colon]))
	key := strings.TrimSpace(raw[colon+1:])
	if key == "" {
		return fmt.Errorf("param override operation %d %s target key is required", index, mode)
	}
	switch kind {
	case "json", "body", "header":
		return nil
	default:
		return fmt.Errorf("param override operation %d %s target prefix is invalid: %s", index, mode, raw)
	}
}

func parseOverrideJSONObject(input string) (map[string]any, error) {
	if strings.TrimSpace(input) == "" {
		return map[string]any{}, nil
	}

	var parsed any
	if err := json.Unmarshal([]byte(input), &parsed); err != nil {
		return nil, fmt.Errorf("must be valid JSON: %w", err)
	}

	obj, ok := parsed.(map[string]any)
	if !ok || obj == nil {
		return nil, fmt.Errorf("override must be a JSON object")
	}

	return obj, nil
}
