package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
)

func TestCanViewRequestContent(t *testing.T) {
	// 无 user（系统内部调用：GC、备份等）→ true
	require.True(t, CanViewRequestContent(context.Background()))

	// 非 owner 注册用户 → false
	regularUser := &ent.User{IsOwner: false}
	require.False(t, CanViewRequestContent(contexts.WithUser(context.Background(), regularUser)))

	// owner → true
	owner := &ent.User{IsOwner: true}
	require.True(t, CanViewRequestContent(contexts.WithUser(context.Background(), owner)))
}

// TestLoadStar_NonOwnerBlocked verifies that non-owner callers cannot read any
// request body / response body / chunks: the owner gate returns empty before
// touching data storage, so a RequestService with a nil DataStorageService does
// not panic and never leaks the stored body.
func TestLoad_NonOwnerBlocked(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	svc := &RequestService{AbstractService: &AbstractService{db: client}}
	req := &ent.Request{
		ID:           1,
		ProjectID:    1,
		Status:       request.StatusCompleted,
		RequestBody:  objects.JSONRawMessage(`{"model":"gpt-4"}`),
		ResponseBody: objects.JSONRawMessage(`{"choices":[]}`),
	}
	exec := &ent.RequestExecution{
		ID:           1,
		ProjectID:    1,
		RequestID:    1,
		Status:       requestexecution.StatusCompleted,
		RequestBody:  objects.JSONRawMessage(`{"model":"claude"}`),
		ResponseBody: objects.JSONRawMessage(`{"content":"secret-exec-resp"}`),
	}

	regularCtx := contexts.WithUser(context.Background(), &ent.User{IsOwner: false})

	got, err := svc.LoadRequestBody(regularCtx, req)
	require.NoError(t, err)
	require.NotContains(t, string(got), "gpt-4")

	resp, err := svc.LoadResponseBody(regularCtx, req)
	require.NoError(t, err)
	require.NotContains(t, string(resp), "choices")

	chunks, err := svc.LoadResponseChunks(regularCtx, req)
	require.NoError(t, err)
	require.Empty(t, chunks)

	execBody, err := svc.LoadRequestExecutionRequestBody(regularCtx, exec)
	require.NoError(t, err)
	require.NotContains(t, string(execBody), "claude")

	execResp, err := svc.LoadRequestExecutionResponseBody(regularCtx, exec)
	require.NoError(t, err)
	require.NotContains(t, string(execResp), "secret-exec-resp")

	execChunks, err := svc.LoadRequestExecutionResponseChunks(regularCtx, exec)
	require.NoError(t, err)
	require.Empty(t, execChunks)
}
