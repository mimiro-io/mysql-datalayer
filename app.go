package mysql

import (
	"context"
	"github.com/mimiro-io/mysql-datalayer/internal/conf"
	"github.com/mimiro-io/mysql-datalayer/internal/layers"
	"github.com/mimiro-io/mysql-datalayer/internal/security"
	"github.com/mimiro-io/mysql-datalayer/internal/web"
	"go.uber.org/fx"
)

func wire() *fx.App {
	app := fx.New(
		fx.Provide(
			conf.NewEnv,
			security.NewTokenProviders,
			conf.NewConfigurationManager,
			conf.NewStatsd,
			conf.NewLogger,
			web.NewWebServer,
			web.NewMiddleware,
			layers.NewLayer,
			layers.NewPostLayer,
		),
		fx.Invoke(
			web.Register,
			web.NewDatasetHandler,
			web.NewPostHandler,
		),
	)
	return app
}

func Run() {
	wire().Run()
}

func Start(ctx context.Context) (*fx.App, error) {
	app := wire()
	err := app.Start(ctx)
	return app, err
}
