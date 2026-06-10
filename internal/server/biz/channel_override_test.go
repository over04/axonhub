package biz

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateParamOverrideJSONRejectsInvalidOperations(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		message string
	}{
		{
			name:    "unsupported mode",
			input:   `{"operations":[{"mode":"typo"}]}`,
			message: "mode is unsupported",
		},
		{
			name:    "set requires value",
			input:   `{"operations":[{"mode":"set","path":"temperature"}]}`,
			message: "set value is required",
		},
		{
			name:    "copy requires to",
			input:   `{"operations":[{"mode":"copy","from":"a"}]}`,
			message: "copy to is required",
		},
		{
			name:    "condition mode is validated",
			input:   `{"operations":[{"mode":"set","path":"temperature","value":0,"conditions":[{"path":"model","mode":"typo","value":"gpt"}]}]}`,
			message: "condition 1 mode is unsupported",
		},
		{
			name:    "return error status is validated",
			input:   `{"operations":[{"mode":"return_error","value":{"message":"blocked","status_code":99}}]}`,
			message: "status code out of range",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateParamOverrideJSON(tt.input)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestValidateParamOverrideJSONAcceptsSupportedOperations(t *testing.T) {
	err := ValidateParamOverrideJSON(`{
		"operations": [
			{"mode":"set","path":"temperature","value":0.2},
			{"mode":"copy","from":"metadata.trace_id","to":"trace_id"},
			{"mode":"set_header","path":"X-Trace-Id","value":"{client_header:X-Trace-Id}"},
			{"mode":"pass_headers","value":{"headers":["X-Client"]}},
			{"mode":"prune_objects","value":{"where":{"type":"debug"}}},
			{"mode":"return_error","value":{"message":"blocked","status_code":429,"skip_retry":false}}
		]
	}`)
	require.NoError(t, err)
}
