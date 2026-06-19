package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

type AuthHandlersParams struct {
	fx.In

	AuthService         *biz.AuthService
	UserService         *biz.UserService
	RegistrationService *biz.RegistrationService
	SystemService       *biz.SystemService
}

func NewAuthHandlers(params AuthHandlersParams) *AuthHandlers {
	return &AuthHandlers{
		AuthService:         params.AuthService,
		UserService:         params.UserService,
		RegistrationService: params.RegistrationService,
		SystemService:       params.SystemService,
	}
}

type AuthHandlers struct {
	AuthService         *biz.AuthService
	UserService         *biz.UserService
	RegistrationService *biz.RegistrationService
	SystemService       *biz.SystemService
}

// SignInRequest 登录请求.
type SignInRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// SignInResponse 登录响应.
type SignInResponse struct {
	User  *objects.UserInfo `json:"user"`
	Token string            `json:"token"`
}

type SignUpRequest struct {
	Email      string `json:"email"      binding:"required,email"`
	Password   string `json:"password"   binding:"required,min=8"`
	FirstName  string `json:"firstName"  binding:"required"`
	LastName   string `json:"lastName"   binding:"required"`
	InviteCode string `json:"inviteCode"`
}

// SignIn handles user authentication.
func (h *AuthHandlers) SignIn(c *gin.Context) {
	var (
		ctx = c.Request.Context()
		req SignInRequest
	)

	err := c.ShouldBindJSON(&req)
	if err != nil {
		JSONError(c, http.StatusBadRequest, errors.New("Invalid request format"))
		return
	}

	// Authenticate user
	user, err := h.AuthService.AuthenticateUser(ctx, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, biz.ErrInvalidPassword) {
			JSONError(c, http.StatusUnauthorized, errors.New("Invalid email or password"))
			return
		}

		JSONError(c, http.StatusInternalServerError, errors.New("Internal server error"))

		return
	}

	if _, err := h.RegistrationService.EnsurePersonalProject(ctx, user.ID); err != nil {
		log.Warn(ctx, "failed to ensure personal project during sign-in", log.Cause(err))
	}

	fullUser, err := authz.RunWithSystemBypass(ctx, "signin-load-user", func(bypassCtx context.Context) (*ent.User, error) {
		return h.UserService.GetUserByID(bypassCtx, user.ID)
	})
	if err != nil {
		log.Error(ctx, "failed to load user during sign-in", log.Cause(err))
		JSONError(c, http.StatusInternalServerError, errors.New("Internal server error"))
		return
	}

	// Generate JWT token
	token, err := h.AuthService.GenerateJWTToken(ctx, fullUser)
	if err != nil {
		JSONError(c, http.StatusInternalServerError, errors.New("Internal server error"))
		return
	}

	response := SignInResponse{
		User:  biz.ConvertUserToUserInfo(ctx, fullUser),
		Token: token,
	}

	c.JSON(http.StatusOK, response)
}

func (h *AuthHandlers) PublicSettings(c *gin.Context) {
	settings, err := h.SystemService.PublicAuthSettings(c.Request.Context())
	if err != nil {
		JSONError(c, http.StatusInternalServerError, errors.New("Internal server error"))
		return
	}

	c.JSON(http.StatusOK, settings)
}

func (h *AuthHandlers) SignUp(c *gin.Context) {
	var req SignUpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		JSONError(c, http.StatusBadRequest, errors.New("Invalid request format"))
		return
	}

	user, err := h.RegistrationService.Register(c.Request.Context(), biz.RegisterUserInput{
		Email:      req.Email,
		Password:   req.Password,
		FirstName:  req.FirstName,
		LastName:   req.LastName,
		InviteCode: req.InviteCode,
	})
	if err != nil {
		switch {
		case errors.Is(err, biz.ErrPublicModeDisabled):
			JSONError(c, http.StatusForbidden, errors.New("Public registration is disabled"))
		case errors.Is(err, biz.ErrInvalidInviteCode):
			JSONError(c, http.StatusUnauthorized, errors.New("Invalid invite code"))
		case errors.Is(err, biz.ErrEmailAlreadyExists):
			JSONError(c, http.StatusConflict, errors.New("Email already exists"))
		case errors.Is(err, biz.ErrSystemNotInitialized):
			JSONError(c, http.StatusBadRequest, errors.New("System is not initialized"))
		default:
			log.Error(c.Request.Context(), "failed to register user", log.Cause(err))
			JSONError(c, http.StatusInternalServerError, errors.New("Internal server error"))
		}
		return
	}

	token, err := h.AuthService.GenerateJWTToken(c.Request.Context(), user)
	if err != nil {
		JSONError(c, http.StatusInternalServerError, errors.New("Internal server error"))
		return
	}

	c.JSON(http.StatusOK, SignInResponse{
		User:  biz.ConvertUserToUserInfo(c.Request.Context(), user),
		Token: token,
	})
}
