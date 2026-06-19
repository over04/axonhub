package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/userproject"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/scopes"
)

func setupTestRegistrationService(t *testing.T) (*RegistrationService, *SystemService, *UserService, *ent.Client, context.Context) {
	t.Helper()

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	cacheConfig := xcache.Config{Mode: xcache.ModeMemory}
	systemService := NewSystemService(SystemServiceParams{CacheConfig: cacheConfig, Ent: client})
	userService := NewUserService(UserServiceParams{CacheConfig: cacheConfig, Ent: client})
	registrationService := NewRegistrationService(RegistrationServiceParams{
		Ent:           client,
		SystemService: systemService,
		UserService:   userService,
	})

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	err := systemService.Initialize(ctx, &InitializeSystemParams{
		OwnerEmail:     "owner@example.com",
		OwnerPassword:  "password123",
		OwnerFirstName: "Owner",
		OwnerLastName:  "User",
		BrandName:      "AxonHub",
	})
	require.NoError(t, err)

	return registrationService, systemService, userService, client, ctx
}

func TestRegistrationService_RegisterPublicModeOff(t *testing.T) {
	registrationService, _, _, client, ctx := setupTestRegistrationService(t)
	defer client.Close()

	_, err := registrationService.Register(ctx, RegisterUserInput{
		Email:    "user@example.com",
		Password: "password123",
	})
	require.ErrorIs(t, err, ErrPublicModeDisabled)
}

func TestRegistrationService_RegisterOpenPublicMode(t *testing.T) {
	registrationService, systemService, _, client, ctx := setupTestRegistrationService(t)
	defer client.Close()

	require.NoError(t, systemService.SetPublicMode(ctx, true))

	u, err := registrationService.Register(ctx, RegisterUserInput{
		Email:     "user@example.com",
		Password:  "password123",
		FirstName: "Alice",
		LastName:  "Smith",
	})
	require.NoError(t, err)
	require.Equal(t, "Alice", u.FirstName)
	require.Equal(t, "Smith", u.LastName)
	require.False(t, u.IsOwner)
	require.Empty(t, u.Scopes)
	require.Len(t, u.Edges.ProjectUsers, 1)

	up := u.Edges.ProjectUsers[0]
	require.False(t, up.IsOwner)
	require.ElementsMatch(t, []string{
		string(scopes.ScopeReadAPIKeys),
		string(scopes.ScopeWriteAPIKeys),
		string(scopes.ScopeReadRequests),
		string(scopes.ScopeWriteRequests),
		string(scopes.ScopeReadPrompts),
		string(scopes.ScopeWritePrompts),
	}, up.Scopes)

	proj, err := client.Project.Get(ctx, up.ProjectID)
	require.NoError(t, err)
	require.Equal(t, "Alice Smith Project", proj.Name)
}

func TestRegistrationService_PersonalProjectNameCollision(t *testing.T) {
	registrationService, systemService, _, client, ctx := setupTestRegistrationService(t)
	defer client.Close()

	require.NoError(t, systemService.SetPublicMode(ctx, true))

	first, err := registrationService.Register(ctx, RegisterUserInput{
		Email:     "first@example.com",
		Password:  "password123",
		FirstName: "Alice",
		LastName:  "Smith",
	})
	require.NoError(t, err)

	second, err := registrationService.Register(ctx, RegisterUserInput{
		Email:     "second@example.com",
		Password:  "password123",
		FirstName: "Alice",
		LastName:  "Smith",
	})
	require.NoError(t, err)

	firstProject, err := client.Project.Get(ctx, first.Edges.ProjectUsers[0].ProjectID)
	require.NoError(t, err)
	secondProject, err := client.Project.Get(ctx, second.Edges.ProjectUsers[0].ProjectID)
	require.NoError(t, err)

	require.Equal(t, "Alice Smith Project", firstProject.Name)
	require.Equal(t, "Alice Smith Project 2", secondProject.Name)
}

func TestRegistrationService_RegisterInviteCode(t *testing.T) {
	registrationService, systemService, _, client, ctx := setupTestRegistrationService(t)
	defer client.Close()

	require.NoError(t, systemService.SetPublicMode(ctx, true))
	require.NoError(t, systemService.SetRegistrationInviteCode(ctx, "secret-code"))

	_, err := registrationService.Register(ctx, RegisterUserInput{
		Email:      "wrong@example.com",
		Password:   "password123",
		InviteCode: "wrong-code",
	})
	require.ErrorIs(t, err, ErrInvalidInviteCode)

	u, err := registrationService.Register(ctx, RegisterUserInput{
		Email:      "right@example.com",
		Password:   "password123",
		InviteCode: "secret-code",
	})
	require.NoError(t, err)
	require.NotZero(t, u.ID)
}

func TestRegistrationService_EnsurePersonalProjectIdempotent(t *testing.T) {
	registrationService, systemService, _, client, ctx := setupTestRegistrationService(t)
	defer client.Close()

	require.NoError(t, systemService.SetPublicMode(ctx, true))

	u, err := registrationService.Register(ctx, RegisterUserInput{
		Email:    "idempotent@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	first, err := registrationService.EnsurePersonalProject(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := registrationService.EnsurePersonalProject(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, first.ID, second.ID)

	count, err := client.UserProject.Query().Where(userproject.UserIDEQ(u.ID)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
