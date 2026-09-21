// Package middleware holds the Hertz middleware: request id, structured access
// logging, panic recovery, permissive CORS, and x-api-key authentication.
package middleware

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/thanhenti/bepilot/internal/api/dto"
	"github.com/thanhenti/bepilot/internal/domain"
	"github.com/thanhenti/bepilot/internal/store"
)

// devBypassEmail/devBypassName identify the fixed local user that stands in
// for authentication when Auth's bypass flag is set.
const (
	devBypassEmail = "dev-bypass@bepilot.local"
	devBypassName  = "Dev Bypass User"
)

type ctxKey string

const (
	keyRequestID ctxKey = "request_id"
	keyUser      ctxKey = "user"
)

// RequestID assigns a request id (honouring an inbound X-Request-Id).
func RequestID() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		id := string(c.GetHeader("X-Request-Id"))
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(string(keyRequestID), id)
		c.Header("X-Request-Id", id)
		c.Next(ctx)
	}
}

// RequestIDFrom returns the request id set by RequestID.
func RequestIDFrom(c *app.RequestContext) string {
	if v, ok := c.Get(string(keyRequestID)); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// AccessLog logs one line per request after it completes.
func AccessLog(log *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		start := time.Now()
		c.Next(ctx)
		log.Info("http",
			"method", string(c.Method()),
			"path", string(c.Path()),
			"status", c.Response.StatusCode(),
			"dur_ms", time.Since(start).Milliseconds(),
			"request_id", RequestIDFrom(c),
		)
	}
}

// Recovery converts a panic into a 500 error envelope.
func Recovery(log *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered", "err", r, "path", string(c.Path()), "request_id", RequestIDFrom(c))
				c.AbortWithStatusJSON(consts.StatusInternalServerError, dto.NewError("internal_server_error", "internal error"))
			}
		}()
		c.Next(ctx)
	}
}

// CORS applies permissive CORS suitable for API usage.
func CORS(origins []string) app.HandlerFunc {
	allowAll := len(origins) == 0
	set := map[string]bool{}
	for _, o := range origins {
		set[o] = true
	}
	return func(ctx context.Context, c *app.RequestContext) {
		origin := string(c.GetHeader("Origin"))
		if allowAll {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if set[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, x-api-key, anthropic-version, X-Request-Id")
		if string(c.Method()) == consts.MethodOptions {
			c.AbortWithStatus(consts.StatusNoContent)
			return
		}
		c.Next(ctx)
	}
}

// Auth resolves the x-api-key header (falling back to Authorization: Bearer) to
// a user and stores it in the request context.
//
// When bypass is true (BEPILOT_AUTH_BYPASS=true), the check is skipped
// entirely: every request is treated as a fixed local dev user, created once
// on first use. This is a local-development convenience only — never enable
// it in a deployed environment, since it removes all request authentication.
func Auth(keys *store.APIKeysRepo, users *store.UsersRepo, bypass bool) app.HandlerFunc {
	var (
		once    sync.Once
		devUser domain.User
		devErr  error
	)
	return func(ctx context.Context, c *app.RequestContext) {
		if bypass {
			once.Do(func() { devUser, devErr = users.Create(ctx, devBypassEmail, devBypassName) })
			if devErr != nil {
				c.AbortWithStatusJSON(consts.StatusInternalServerError, dto.NewError("internal_server_error", "auth bypass: "+devErr.Error()))
				return
			}
			c.Set(string(keyUser), devUser)
			c.Next(ctx)
			return
		}

		raw := string(c.GetHeader("x-api-key"))
		if raw == "" {
			if b := string(c.GetHeader("Authorization")); len(b) > 7 && b[:7] == "Bearer " {
				raw = b[7:]
			}
		}
		if raw == "" {
			c.AbortWithStatusJSON(consts.StatusUnauthorized, dto.NewError("authentication_error", "missing x-api-key header"))
			return
		}
		user, err := keys.Verify(ctx, raw)
		if err != nil {
			c.AbortWithStatusJSON(consts.StatusUnauthorized, dto.NewError("authentication_error", "invalid api key"))
			return
		}
		c.Set(string(keyUser), user)
		c.Next(ctx)
	}
}

// UserFrom returns the authenticated user, or ok=false.
func UserFrom(c *app.RequestContext) (domain.User, bool) {
	v, ok := c.Get(string(keyUser))
	if !ok {
		return domain.User{}, false
	}
	u, ok := v.(domain.User)
	return u, ok
}

var _ = utils.H{}
