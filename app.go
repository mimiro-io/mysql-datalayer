package mysql

import (
	"context"
	conf2 "github.com/mimiro-io/mysql-datalayer/internal/legacy/conf"
	layers2 "github.com/mimiro-io/mysql-datalayer/internal/legacy/layers"
	"github.com/mimiro-io/mysql-datalayer/internal/legacy/security"
	web2 "github.com/mimiro-io/mysql-datalayer/internal/legacy/web"
	"go.uber.org/fx"
)

func wire() *fx.App {
	app := fx.New(
		fx.Provide(
			conf2.NewEnv,
			security.NewTokenProviders,
			conf2.NewConfigurationManager,
			conf2.NewStatsd,
			conf2.NewLogger,
			web2.NewWebServer,
			web2.NewMiddleware,
			layers2.NewLayer,
			layers2.NewPostLayer,
		),
		fx.Invoke(
			web2.Register,
			web2.NewDatasetHandler,
			web2.NewPostHandler,
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
