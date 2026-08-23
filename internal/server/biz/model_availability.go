package biz

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xtime"
)

// ModelHealthPoint is one time bucket of a model's availability, used for the
// bar chart. Same granularity as channel probe points.
type ModelHealthPoint struct {
	Timestamp       float64
	TotalRequests   int
	SuccessRequests int
	AvgLatencyMs    *float64
}

// ModelAvailabilityInfo is a sanitized, channel-free view of a model's
// availability: latest request status/latency, window success rate & avg
// latency, and per-bucket health points for the bar chart. It never carries
// channel identity, provider, credentials, or pricing.
type ModelAvailabilityInfo struct {
	ModelID         string
	DisplayName     string
	Icon            string
	Available       bool
	SuccessRate     *float64
	AvgLatencyMs    *float64
	Capabilities    *objects.ModelCard
	LatestStatus    string // success | error | unknown
	LatestLatencyMs *float64
	HealthPoints    []ModelHealthPoint
}

// ListModelAvailability returns every enabled model with sanitized, channel-free
// availability info. It runs under a system bypass so it can aggregate request
// metrics across projects without leaking channel identity — the output is
// strictly model-dimension.
func (svc *ModelService) ListModelAvailability(ctx context.Context) ([]*ModelAvailabilityInfo, error) {
	return authz.RunWithSystemBypass(ctx, "model-availability", func(bypassCtx context.Context) ([]*ModelAvailabilityInfo, error) {
		models, err := svc.ListEnabledModels(bypassCtx)
		if err != nil {
			return nil, err
		}

		modelIDs := lo.Map(models, func(m ModelFacade, _ int) string { return m.ID })

		cardMap := make(map[string]*objects.ModelCard, len(modelIDs))
		iconMap := make(map[string]string, len(modelIDs))
		if len(modelIDs) > 0 {
			if configured, qErr := svc.entFromContext(bypassCtx).Model.Query().
				Where(model.ModelIDIn(modelIDs...)).
				All(bypassCtx); qErr == nil {
				for _, m := range configured {
					cardMap[m.ModelID] = m.ModelCard
					if m.Icon != "" {
						iconMap[m.ModelID] = m.Icon
					}
				}
			}
		}

		healthMap, latestMap, aggErr := svc.aggregateModelAvailability(bypassCtx, modelIDs)
		if aggErr != nil {
			return nil, aggErr
		}

		result := make([]*ModelAvailabilityInfo, 0, len(models))
		for _, m := range models {
			points := healthMap[m.ID]
			info := &ModelAvailabilityInfo{
				ModelID:      m.ID,
				DisplayName:  m.DisplayName,
				Icon:         iconMap[m.ID],
				Available:    true,
				Capabilities: cardMap[m.ID],
				HealthPoints: points,
			}

			// Window roll-up (success rate & avg latency) derived from the
			// per-bucket health points, weighted by request count.
			var total, success, latCount int
			var latSum float64
			for _, p := range points {
				total += p.TotalRequests
				success += p.SuccessRequests
				if p.AvgLatencyMs != nil && p.TotalRequests > 0 {
					latSum += *p.AvgLatencyMs * float64(p.TotalRequests)
					latCount += p.TotalRequests
				}
			}
			if total > 0 {
				rate := float64(success) / float64(total)
				info.SuccessRate = &rate
			}
			if latCount > 0 {
				avg := latSum / float64(latCount)
				info.AvgLatencyMs = &avg
			}

			if latest, ok := latestMap[m.ID]; ok {
				info.LatestStatus = latest.status
				info.LatestLatencyMs = latest.latency
			} else {
				info.LatestStatus = "unknown"
			}

			result = append(result, info)
		}
		return result, nil
	})
}

type modelLatestInfo struct {
	status  string
	latency *float64
}

// aggregateModelAvailability returns per-model health points over the same
// time-bucket granularity as /channels probe data (SystemChannelSettings.Probe
// range/interval), keyed by modelID, plus the single latest request per model.
// Missing buckets are filled with zero points so the bar chart has a continuous
// timeline — exactly like ChannelProbeService.QueryChannelProbes.
func (svc *ModelService) aggregateModelAvailability(ctx context.Context, modelIDs []string) (map[string][]ModelHealthPoint, map[string]modelLatestInfo, error) {
	healthMap := map[string][]ModelHealthPoint{}
	latestMap := map[string]modelLatestInfo{}
	if len(modelIDs) == 0 {
		return healthMap, latestMap, nil
	}

	client := svc.entFromContext(ctx)
	setting := svc.systemService.ChannelSettingOrDefault(ctx).Probe
	rangeMinutes := setting.GetQueryRangeMinutes()
	intervalMinutes := setting.GetIntervalMinutes()
	now := xtime.UTCNow()
	// Align to the same interval boundary as channel probes.
	endTime := now.Truncate(time.Duration(intervalMinutes) * time.Minute)
	startTime := endTime.Add(-time.Duration(rangeMinutes) * time.Minute)
	// All expected bucket timestamps, ascending.
	var timestamps []int64
	for t := startTime.Unix(); t <= endTime.Unix(); t += int64(intervalMinutes * 60) {
		timestamps = append(timestamps, t)
	}

	// Aggregate requests into the same interval buckets. A request falls in bucket
	// ts when createdAt ∈ [ts, ts+interval). We compute the bucket via integer
	// division on the unix epoch so it is dialect-agnostic and matches the
	// aligned timestamps above.
	intervalSecs := int64(intervalMinutes * 60)
	bucketStartUnix := startTime.Unix()
	type bucketRow struct {
		Bucket     int64    `json:"bucket"`
		ModelID    string   `json:"model_id"`
		Total      int      `json:"total"`
		Completed  int      `json:"completed"`
		AvgLatency *float64 `json:"avg_latency"`
	}
	var rows []bucketRow
	if err := client.Request.Query().
		Where(request.ModelIDIn(modelIDs...), request.CreatedAtGTE(startTime), request.CreatedAtLTE(endTime)).
		Modify(func(s *sql.Selector) {
			createdAtCol := s.C(request.FieldCreatedAt)
			modelCol := s.C(request.FieldModelID)
			// bucket = floor((createdAt_unix - bucketStartUnix) / intervalSecs)
			// createdAt is stored as a string timestamp; cast to unix seconds first.
			var createdAtUnix string
			switch s.Dialect() {
			case dialect.SQLite:
				createdAtUnix = fmt.Sprintf("strftime('%%s', substr(%s, 1, 19))", createdAtCol)
			case dialect.MySQL:
				createdAtUnix = fmt.Sprintf("UNIX_TIMESTAMP(%s)", createdAtCol)
			case dialect.Postgres:
				createdAtUnix = fmt.Sprintf("EXTRACT(EPOCH FROM %s)::bigint", createdAtCol)
			default:
				createdAtUnix = fmt.Sprintf("UNIX_TIMESTAMP(%s)", createdAtCol)
			}
			bucketExpr := fmt.Sprintf("FLOOR((%s - %d) / %d)", createdAtUnix, bucketStartUnix, intervalSecs)
			s.Select(
				sql.As(bucketExpr, "bucket"),
				sql.As(modelCol, "model_id"),
				sql.As(sql.Count(s.C(request.FieldID)), "total"),
				sql.As(fmt.Sprintf("SUM(CASE WHEN %s = 'completed' THEN 1 ELSE 0 END)", s.C(request.FieldStatus)), "completed"),
				sql.As(fmt.Sprintf("AVG(%s)", s.C(request.FieldMetricsLatencyMs)), "avg_latency"),
			).GroupBy(modelCol, bucketExpr)
		}).Scan(ctx, &rows); err != nil {
		return nil, nil, fmt.Errorf("failed to aggregate model availability: %w", err)
	}

	// modelID -> bucket index -> row
	bucketMap := map[string]map[int64]bucketRow{}
	for _, r := range rows {
		if bucketMap[r.ModelID] == nil {
			bucketMap[r.ModelID] = map[int64]bucketRow{}
		}
		bucketMap[r.ModelID][r.Bucket] = r
	}

	for _, id := range modelIDs {
		points := make([]ModelHealthPoint, 0, len(timestamps))
		bm := bucketMap[id]
		for i, ts := range timestamps {
			var avgLat *float64
			if row, ok := bm[int64(i)]; ok {
				avgLat = row.AvgLatency
				points = append(points, ModelHealthPoint{
					Timestamp:       float64(ts),
					TotalRequests:   row.Total,
					SuccessRequests: row.Completed,
					AvgLatencyMs:    avgLat,
				})
			} else {
				// Fill missing bucket with zero, like channel probes.
				points = append(points, ModelHealthPoint{
					Timestamp:     float64(ts),
					TotalRequests: 0,
				})
			}
		}
		healthMap[id] = points
	}

	// Latest request per model. Two batched queries replace the previous
	// per-model loop (N queries). id is the auto-increment primary key, so
	// MAX(id) per model_id is that model's most recent row. Splitting into a
	// grouped MAX query + a primary-key fetch avoids correlated-subquery table
	// aliasing (the inner FROM would shadow the outer table, collapsing MAX to a
	// global maximum) and keeps the work to two queries regardless of model count.
	type maxIDRow struct {
		ModelID string `json:"model_id"`
		MaxID   int    `json:"max_id"`
	}
	var maxRows []maxIDRow
	if err := client.Request.Query().
		Where(request.ModelIDIn(modelIDs...)).
		Modify(func(s *sql.Selector) {
			s.Select(
				sql.As(s.C(request.FieldModelID), "model_id"),
				sql.As(fmt.Sprintf("MAX(%s)", s.C(request.FieldID)), "max_id"),
			).GroupBy(s.C(request.FieldModelID))
		}).Scan(ctx, &maxRows); err != nil {
		return nil, nil, fmt.Errorf("failed to get latest request ids: %w", err)
	}
	if len(maxRows) > 0 {
		maxIDs := lo.Map(maxRows, func(r maxIDRow, _ int) int { return r.MaxID })
		latestReqs, err := client.Request.Query().
			Where(request.IDIn(maxIDs...)).
			All(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get latest requests: %w", err)
		}
		for _, req := range latestReqs {
			li := modelLatestInfo{status: "unknown"}
			switch req.Status {
			case request.StatusCompleted:
				li.status = "success"
			case request.StatusFailed, request.StatusCanceled:
				li.status = "error"
			}
			if req.MetricsLatencyMs != nil {
				lat := float64(*req.MetricsLatencyMs)
				li.latency = &lat
			}
			latestMap[req.ModelID] = li
		}
	}

	return healthMap, latestMap, nil
}
