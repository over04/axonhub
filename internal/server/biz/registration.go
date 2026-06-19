package biz

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/fx"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/user"
	"github.com/looplj/axonhub/internal/scopes"
)

type RegistrationServiceParams struct {
	fx.In

	Ent           *ent.Client
	SystemService *SystemService
	UserService   *UserService
}

type RegistrationService struct {
	*AbstractService

	SystemService *SystemService
	UserService   *UserService
}

type RegisterUserInput struct {
	Email      string
	Password   string
	FirstName  string
	LastName   string
	InviteCode string
}

func NewRegistrationService(params RegistrationServiceParams) *RegistrationService {
	return &RegistrationService{
		AbstractService: &AbstractService{db: params.Ent},
		SystemService:   params.SystemService,
		UserService:     params.UserService,
	}
}

func (s *RegistrationService) Register(ctx context.Context, input RegisterUserInput) (*ent.User, error) {
	ctx = authz.WithSystemBypass(ctx, "public-register")

	isInitialized, err := s.SystemService.IsInitialized(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check system initialization: %w", err)
	}
	if !isInitialized {
		return nil, ErrSystemNotInitialized
	}

	publicMode, err := s.SystemService.PublicMode(ctx)
	if err != nil {
		return nil, err
	}
	if !publicMode {
		return nil, ErrPublicModeDisabled
	}

	expectedInviteCode, err := s.SystemService.RegistrationInviteCode(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(expectedInviteCode) != "" && strings.TrimSpace(input.InviteCode) != strings.TrimSpace(expectedInviteCode) {
		return nil, ErrInvalidInviteCode
	}

	email := strings.TrimSpace(strings.ToLower(input.Email))
	exists, err := s.entFromContext(ctx).User.Query().
		Where(user.EmailEQ(email)).
		Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check user email: %w", err)
	}
	if exists {
		return nil, ErrEmailAlreadyExists
	}

	var createdUser *ent.User
	err = s.RunInTransaction(ctx, func(txCtx context.Context) error {
		hashedPassword, err := HashPassword(input.Password)
		if err != nil {
			return fmt.Errorf("failed to hash password: %w", err)
		}

		createdUser, err = s.entFromContext(txCtx).User.Create().
			SetEmail(email).
			SetPassword(hashedPassword).
			SetFirstName(normalizeDisplayName(input.FirstName)).
			SetLastName(normalizeDisplayName(input.LastName)).
			SetIsOwner(false).
			SetScopes([]string{}).
			Save(txCtx)
		if err != nil {
			if ent.IsConstraintError(err) {
				return ErrEmailAlreadyExists
			}
			return fmt.Errorf("failed to create user: %w", err)
		}

		_, err = s.createPersonalProject(txCtx, createdUser)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.UserService.GetUserByID(ctx, createdUser.ID)
}

func (s *RegistrationService) EnsurePersonalProject(ctx context.Context, userID int) (*ent.Project, error) {
	return authz.RunWithSystemBypass(ctx, "ensure-personal-project", func(bypassCtx context.Context) (*ent.Project, error) {
		publicMode, err := s.SystemService.PublicMode(bypassCtx)
		if err != nil {
			return nil, err
		}
		if !publicMode {
			return nil, nil
		}

		u, err := s.entFromContext(bypassCtx).User.Get(bypassCtx, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to get user: %w", err)
		}
		if u.IsOwner {
			return nil, nil
		}

		existing, err := s.entFromContext(bypassCtx).Project.Query().
			Where(project.StatusEQ(project.StatusActive)).
			Where(project.HasUsersWith(user.IDEQ(userID))).
			First(bypassCtx)
		if err == nil {
			return existing, nil
		}
		if !ent.IsNotFound(err) {
			return nil, fmt.Errorf("failed to query user projects: %w", err)
		}

		var created *ent.Project
		err = s.RunInTransaction(bypassCtx, func(txCtx context.Context) error {
			created, err = s.createPersonalProject(txCtx, u)
			return err
		})
		if err != nil {
			return nil, err
		}

		s.UserService.invalidateUserCache(bypassCtx, userID)

		return created, nil
	})
}

func (s *RegistrationService) createPersonalProject(ctx context.Context, u *ent.User) (*ent.Project, error) {
	client := s.entFromContext(ctx)
	name, err := s.availablePersonalProjectName(ctx, u)
	if err != nil {
		return nil, err
	}

	proj, err := client.Project.Create().
		SetName(name).
		SetDescription("Personal project").
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create personal project: %w", err)
	}

	_, err = client.UserProject.Create().
		SetUserID(u.ID).
		SetProjectID(proj.ID).
		SetIsOwner(false).
		SetScopes(publicUserProjectScopes()).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to assign personal project: %w", err)
	}

	return proj, nil
}

func (s *RegistrationService) availablePersonalProjectName(ctx context.Context, u *ent.User) (string, error) {
	baseName := personalProjectName(u)
	client := s.entFromContext(ctx)

	for i := 0; i < 100; i++ {
		name := baseName
		if i > 0 {
			name = fmt.Sprintf("%s %d", baseName, i+1)
		}

		exists, err := client.Project.Query().
			Where(project.Name(name)).
			Exist(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to check personal project name: %w", err)
		}
		if !exists {
			return name, nil
		}
	}

	return fmt.Sprintf("%s %d", baseName, u.ID), nil
}

func personalProjectName(u *ent.User) string {
	name := normalizeDisplayName(strings.Join([]string{u.FirstName, u.LastName}, " "))
	if name == "" {
		local, _, _ := strings.Cut(u.Email, "@")
		name = normalizeDisplayName(local)
	}
	if name == "" {
		name = "User"
	}

	return fmt.Sprintf("%s Project", name)
}

func normalizeDisplayName(name string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
}

func publicUserProjectScopes() []string {
	return []string{
		string(scopes.ScopeReadAPIKeys),
		string(scopes.ScopeWriteAPIKeys),
		string(scopes.ScopeReadRequests),
		string(scopes.ScopeWriteRequests),
		string(scopes.ScopeReadPrompts),
		string(scopes.ScopeWritePrompts),
	}
}
