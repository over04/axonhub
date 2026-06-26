package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
)

// testSystemSvc builds a SystemService whose ChannelSettingOrDefault returns
// the in-memory defaults (no System row -> defaultChannelSetting).
func testSystemSvc(client *ent.Client) *SystemService {
	return &SystemService{
		AbstractService: &AbstractService{db: client},
		Cache:           xcache.NewFromConfig[ent.System](xcache.Config{Mode: xcache.ModeMemory}),
	}
}

// TestAggregateModelAvailability_LatestPerModel guards against the correlated-
// subquery regression: each model must report its OWN most recent request, not
// the global newest. Two models, each with a request; the older global request
// belongs to model A. Before the fix model A's latest collapsed to the global
// MAX and model B got "unknown".
func TestAggregateModelAvailability_LatestPerModel(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	mkReq := func(model string, status request.Status, at time.Time, latency *int64) {
		b := client.Request.Create().
			SetModelID(model).
			SetRequestBody(objects.JSONRawMessage(`{}`)).
			SetStatus(status).
			SetCreatedAt(at)
		if latency != nil {
			b = b.SetMetricsLatencyMs(*latency)
		}
		b.SaveX(ctx)
	}

	// Model A: an old failed request, then a newer completed one.
	lat500 := int64(500)
	lat900 := int64(900)
	mkReq("model-a", request.StatusFailed, base, nil)
	mkReq("model-a", request.StatusCompleted, base.Add(2*time.Hour), &lat500)
	// Model B: a single request newer than both of A's.
	mkReq("model-b", request.StatusFailed, base.Add(3*time.Hour), &lat900)

	svc := &ModelService{
		AbstractService: &AbstractService{db: client},
		systemService:   testSystemSvc(client),
	}

	_, latestMap, err := svc.aggregateModelAvailability(ctx, []string{"model-a", "model-b"})
	require.NoError(t, err)

	// Model A's latest must be its OWN newest (completed at +2h), not the global
	// newest (model B's failed at +3h).
	a, ok := latestMap["model-a"]
	require.True(t, ok, "model-a must have a latest entry")
	require.Equal(t, "success", a.status, "model-a latest should be its completed request")
	require.NotNil(t, a.latency)
	require.InDelta(t, 500.0, *a.latency, 0.001)

	// Model B's latest must be its failed request.
	b, ok := latestMap["model-b"]
	require.True(t, ok, "model-b must have a latest entry")
	require.Equal(t, "error", b.status)
	require.NotNil(t, b.latency)
	require.InDelta(t, 900.0, *b.latency, 0.001)
}

// TestAggregateModelAvailability_BucketIntegerDivision guards the MySQL FLOOR
// fix indirectly: under SQLite the bucket expression must scan into int64
// without error and aggregate same-bucket rows together.
func TestAggregateModelAvailability_BucketIntegerDivision(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())
	sysSvc := testSystemSvc(client)
	setting := sysSvc.ChannelSettingOrDefault(ctx).Probe
	interval := time.Duration(setting.GetIntervalMinutes()) * time.Minute
	endTime := time.Now().UTC().Truncate(interval)
	bucketMid := endTime.Add(-interval / 2)

	// Two requests in the same bucket -> must aggregate into one point.
	client.Request.Create().SetModelID("m1").SetRequestBody(objects.JSONRawMessage(`{}`)).SetStatus(request.StatusCompleted).SetCreatedAt(bucketMid).SaveX(ctx)
	client.Request.Create().SetModelID("m1").SetRequestBody(objects.JSONRawMessage(`{}`)).SetStatus(request.StatusCompleted).SetCreatedAt(bucketMid.Add(1 * time.Second)).SaveX(ctx)

	svc := &ModelService{
		AbstractService: &AbstractService{db: client},
		systemService:   sysSvc,
	}

	healthMap, _, err := svc.aggregateModelAvailability(ctx, []string{"m1"})
	require.NoError(t, err)

	var nonEmpty int
	var totalReqs int
	for _, p := range healthMap["m1"] {
		if p.TotalRequests > 0 {
			nonEmpty++
			totalReqs += p.TotalRequests
		}
	}
	require.Equal(t, 1, nonEmpty, "two same-bucket requests must aggregate into one point")
	require.Equal(t, 2, totalReqs)
}
