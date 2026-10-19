package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

func TestModelMapping_SharedState(t *testing.T) {
	profile := &objects.APIKeyProfiles{
		ActiveProfile: "active",
		Profiles: []objects.APIKeyProfile{{
			Name: "active",
			ModelMappings: []objects.ModelMapping{
				{From: "client-alias", To: "routing-alias"},
			},
		}},
	}
	for _, tt := range []struct {
		name          string
		apiKey        *ent.APIKey
		model         string
		originalModel string
		wantModel     string
	}{
		{name: "nil key captures shared model", model: "client-alias", wantModel: "client-alias"},
		{name: "plain key", apiKey: &ent.APIKey{}, model: "client-alias", wantModel: "client-alias"},
		{name: "inactive profile", apiKey: &ent.APIKey{Profiles: &objects.APIKeyProfiles{Profiles: profile.Profiles}}, model: "client-alias", wantModel: "client-alias"},
		{name: "missing profile", apiKey: &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "missing", Profiles: profile.Profiles}}, model: "client-alias", wantModel: "client-alias"},
		{name: "active mapping", apiKey: &ent.APIKey{Profiles: profile}, model: "client-alias", wantModel: "routing-alias"},
		{name: "active nonmatch", apiKey: &ent.APIKey{Profiles: profile}, model: "other-alias", wantModel: "other-alias"},
		{name: "nil key restores routing model", model: "provider-model", originalModel: "routing-alias", wantModel: "routing-alias"},
		{name: "profile preserves routing model", apiKey: &ent.APIKey{Profiles: profile}, model: "client-alias", originalModel: "saved-alias", wantModel: "saved-alias"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Given an optional profile and shared routing state.
			state := &PersistenceState{APIKey: tt.apiKey, ModelMapper: NewModelMapper(), OriginalModel: tt.originalModel}
			middleware := &apiKeyModelMappingMiddleware{inbound: &PersistentInboundTransformer{state: state}}
			request := &llm.Request{Model: tt.model}

			// When the inbound model mapping stage runs.
			got, err := middleware.OnInboundLlmRequest(t.Context(), request)

			// Then routing uses the post-profile model while responses retain the client model.
			require.NoError(t, err)
			require.Same(t, request, got)
			require.Equal(t, tt.wantModel, got.Model)
			require.Equal(t, tt.wantModel, state.OriginalModel)
			require.Equal(t, tt.model, middleware.RequestModel)
		})
	}
}

func TestModelMapping_RejectsEmptyModel(t *testing.T) {
	// Given an empty model and no API key.
	state := &PersistenceState{ModelMapper: NewModelMapper()}
	middleware := &apiKeyModelMappingMiddleware{inbound: &PersistentInboundTransformer{state: state}}
	// When the mapping stage validates the request.
	got, err := middleware.OnInboundLlmRequest(t.Context(), &llm.Request{})
	// Then no shared state is initialized for the invalid request.
	require.ErrorIs(t, err, biz.ErrInvalidModel)
	require.Nil(t, got)
	require.Empty(t, state.OriginalModel)
	require.Empty(t, middleware.RequestModel)
}
