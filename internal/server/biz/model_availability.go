package biz

import (
	"context"
	"fmt"

	"entgo.io/ent/dialect/sql"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
)

// ModelAvailabilityInfo is a sanitized, channel-free view of a model's
// availability, safe to expose to registered (non-owner) users. It never
// carries channel identity, provider, credentials, or pricing — only model
// dimension: display name, capabilities, success rate, average latency.
type ModelAvailabilityInfo struct {
	ModelID      string
	DisplayName  string
	Available    bool
	SuccessRate  *float64
	AvgLatencyMs *float64
	Capabilities *objects.ModelCard
}

// ListModelAvailability returns every enabled model with sanitized availability
// info. The model list and capabilities are always gathered globally (a system
// bypass) since "which models exist" is platform-level. The success-rate and
// latency aggregation depends on projectScoped:
//   - false: aggregate across the whole system (bypass ctx)
//   - true:  aggregate only the caller's own project (original caller ctx,
//     restricted by ent privacy) so a registered user sees their own per-model
//     usage rather than everyone's.
func (svc *ModelService) ListModelAvailability(ctx context.Context, projectScoped bool) ([]*ModelAvailabilityInfo, error) {
	return authz.RunWithSystemBypass(ctx, "model-availability", func(bypassCtx context.Context) ([]*ModelAvailabilityInfo, error) {
		models, err := svc.ListEnabledModels(bypassCtx)
		if err != nil {
			return nil, err
		}

		modelIDs := lo.Map(models, func(m ModelFacade, _ int) string { return m.ID })

		// Capabilities come from configured Model entities (ModelCard). Channel-
		// derived models have no ModelCard, so their Capabilities stays nil.
		cardMap := make(map[string]*objects.ModelCard, len(modelIDs))
		if len(modelIDs) > 0 {
			if configured, qErr := svc.entFromContext(bypassCtx).Model.Query().
				Where(model.ModelIDIn(modelIDs...)).
				All(bypassCtx); qErr == nil {
				for _, m := range configured {
					cardMap[m.ModelID] = m.ModelCard
				}
			}
		}

		// Aggregation context: project scope uses the caller's original ctx so
		// ent privacy (UserProjectScopeReadRule) restricts Request rows to the
		// caller's project; global scope uses the bypass ctx for a system-wide
		// roll-up.
		aggCtx := bypassCtx
		if projectScoped {
			aggCtx = ctx
		}
		statMap, latMap, aggErr := svc.aggregateModelAvailability(aggCtx, modelIDs)
		if aggErr != nil {
			return nil, aggErr
		}

		result := make([]*ModelAvailabilityInfo, 0, len(models))
		for _, m := range models {
			info := &ModelAvailabilityInfo{
				ModelID:      m.ID,
				DisplayName:  m.DisplayName,
				Available:    true,
				Capabilities: cardMap[m.ID],
			}
			if s, ok := statMap[m.ID]; ok && s.Total > 0 {
				rate := float64(s.Completed) / float64(s.Total)
				info.SuccessRate = &rate
			}
			if lat, ok := latMap[m.ID]; ok {
				info.AvgLatencyMs = &lat
			}
			result = append(result, info)
		}
		return result, nil
	})
}

type modelAvailabilityStat struct {
	ModelID   string `json:"model_id"`
	Total     int    `json:"total"`
	Completed int    `json:"completed"`
}

func (svc *ModelService) aggregateModelAvailability(ctx context.Context, modelIDs []string) (map[string]modelAvailabilityStat, map[string]float64, error) {
	stats := map[string]modelAvailabilityStat{}
	latencies := map[string]float64{}

	if len(modelIDs) == 0 {
		return stats, latencies, nil
	}

	client := svc.entFromContext(ctx)

	// total + completed per model in a single grouped query (CASE WHEN).
	var rows []modelAvailabilityStat
	if err := client.Request.Query().
		Where(request.ModelIDIn(modelIDs...)).
		Modify(func(s *sql.Selector) {
			s.Select(
				s.C(request.FieldModelID),
				sql.As("COUNT(*)", "total"),
				sql.As(fmt.Sprintf("SUM(CASE WHEN %s = 'completed' THEN 1 ELSE 0 END)", s.C(request.FieldStatus)), "completed"),
			).GroupBy(s.C(request.FieldModelID))
		}).Scan(ctx, &rows); err != nil {
		return nil, nil, fmt.Errorf("failed to aggregate model availability: %w", err)
	}
	for _, r := range rows {
		stats[r.ModelID] = r
	}

	// average latency per model (nullable latency column; AVG ignores NULLs).
	type latencyRow struct {
		ModelID string  `json:"model_id"`
		Avg     float64 `json:"avg_latency"`
	}
	var latRows []latencyRow
	if err := client.Request.Query().
		Where(request.ModelIDIn(modelIDs...)).
		Modify(func(s *sql.Selector) {
			s.Select(
				s.C(request.FieldModelID),
				sql.As(fmt.Sprintf("AVG(%s)", s.C(request.FieldMetricsLatencyMs)), "avg_latency"),
			).GroupBy(s.C(request.FieldModelID))
		}).Scan(ctx, &latRows); err != nil {
		return nil, nil, fmt.Errorf("failed to aggregate model latency: %w", err)
	}
	for _, r := range latRows {
		latencies[r.ModelID] = r.Avg
	}

	return stats, latencies, nil
}
