package web

import (
	"context"
	"github.com/mimiro-io/mysql-datalayer/internal/legacy/conf"
	middlewares2 "github.com/mimiro-io/mysql-datalayer/internal/legacy/web/middlewares"
	"strings"

	"github.com/labstack/echo/v4"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type Middleware struct {
	logger     echo.MiddlewareFunc
	jwt        echo.MiddlewareFunc
	recover    echo.MiddlewareFunc
	authorizer func(logger *zap.SugaredLogger, scopes ...string) echo.MiddlewareFunc
	handler    *Handler
	env        *conf.Env
}

func NewMiddleware(lc fx.Lifecycle, handler *Handler, e *echo.Echo, env *conf.Env) *Middleware {
	skipper := func(c echo.Context) bool {
		// don't secure health endpoints
		if strings.HasPrefix(c.Request().URL.Path, "/health") {
			return true
		}
		return false
	}

	mw := &Middleware{
		logger:     setupLogger(handler, skipper),
		jwt:        setupJWT(env, skipper),
		recover:    setupRecovery(handler),
		authorizer: middlewares2.Authorize,
		handler:    handler,
		env:        env,
	}

	if env.Auth.Middleware == "noop" { // don't enable local security if noop is enabled
		handler.Logger.Infof("WARNING: Setting NoOp Authorizer")
		mw.authorizer = middlewares2.NoOpAuthorizer
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			mw.configure(e)
			return nil
		},
	})

	return mw
}

func (middleware *Middleware) configure(e *echo.Echo) {
	e.Use(middleware.logger)
	if middleware.env.Auth.Middleware == "noop" { // don't enable local security (yet)
		middleware.handler.Logger.Infof("WARNING: Security is disabled")
	} else {
		e.Use(middleware.jwt)
	}
	e.Use(middleware.recover)
}

func setupJWT(env *conf.Env, skipper func(c echo.Context) bool) echo.MiddlewareFunc {
	return middlewares2.JWTHandler(&middlewares2.Auth0Config{
		Skipper:       skipper,
		Audience:      env.Auth.Audience,
		Issuer:        env.Auth.Issuer,
		AudienceAuth0: env.Auth.AudienceAuth0,
		IssuerAuth0:   env.Auth.IssuerAuth0,
		Wellknown:     env.Auth.WellKnown,
	})
}

func setupLogger(handler *Handler, skipper func(c echo.Context) bool) echo.MiddlewareFunc {
	return middlewares2.LoggerFilter(middlewares2.LoggerConfig{
		Skipper:      skipper,
		Logger:       handler.Logger.Desugar(),
		StatsdClient: handler.StatsDClient,
	})
}

func setupRecovery(handler *Handler) echo.MiddlewareFunc {
	return middlewares2.RecoverWithConfig(middlewares2.DefaultRecoverConfig, handler.Logger)
}
