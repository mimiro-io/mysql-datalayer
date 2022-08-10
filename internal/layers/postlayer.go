package layers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/mimiro-io/mysql-datalayer/internal/conf"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"net/url"
	"sort"
	"strings"
)

type PostLayer struct {
	cmgr     *conf.ConfigurationManager //
	logger   *zap.SugaredLogger
	PostRepo *PostRepository //exported because it needs to deferred from main??
}
type PostRepository struct {
	DB           *sql.DB
	ctx          context.Context
	postTableDef *conf.PostMapping
	digest       [16]byte
}

func NewPostLayer(lc fx.Lifecycle, cmgr *conf.ConfigurationManager, logger *zap.SugaredLogger) *PostLayer {
	postLayer := &PostLayer{logger: logger.Named("layer")}
	postLayer.cmgr = cmgr
	postLayer.PostRepo = &PostRepository{
		ctx: context.Background(),
	}

	_ = postLayer.ensureConnection()

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			if postLayer.PostRepo.DB != nil {
				postLayer.PostRepo.DB.Close()
			}
			return nil
		},
	})

	return postLayer
}

func (postLayer *PostLayer) connect() (*sql.DB, error) {

	u := &url.URL{
		Scheme: "mysql",
		User:   url.UserPassword(postLayer.PostRepo.postTableDef.Config.User.GetValue(), postLayer.PostRepo.postTableDef.Config.Password.GetValue()),
		Host:   fmt.Sprintf("%s:%s", *postLayer.PostRepo.postTableDef.Config.DatabaseServer, *postLayer.PostRepo.postTableDef.Config.Port),
		Path:   *postLayer.PostRepo.postTableDef.Config.Database,
	}

	db, err := sql.Open(u.Scheme, fmt.Sprintf("%v:%v@(%v)/%v?%s", u.User.Username(),  postLayer.PostRepo.postTableDef.Config.Password.GetValue(), u.Host, u.Path, "parseTime=true"))

	if err != nil {
		postLayer.logger.Warn("Error creating connection: ", err.Error())
		return nil, err
	}

	err = db.PingContext(postLayer.PostRepo.ctx)
	if err != nil {
		postLayer.logger.Warn(err.Error())
		return nil, err
	}

	return db, nil
}

func (postLayer *PostLayer) PostEntities(datasetName string, entities []*Entity) error {

	postLayer.PostRepo.postTableDef = postLayer.GetTableDefinition(datasetName)

	if postLayer.PostRepo.postTableDef == nil {
		return errors.New(fmt.Sprintf("No configuration found for dataset: %s", datasetName))
	}

	if postLayer.PostRepo.DB == nil {
		db, err := postLayer.connect() // errors are already logged
		if err != nil {
			return err
		}
		postLayer.PostRepo.DB = db
	}

	query := postLayer.PostRepo.postTableDef.Query
	if query == "" {
		postLayer.logger.Errorf("Please add query in config for %s in ", datasetName)
		return errors.New(fmt.Sprintf("no query found in config for dataset: %s", datasetName))
	}
	postLayer.logger.Debug(query)

	queryDel := fmt.Sprintf(`DELETE FROM %s WHERE id = ?;`, strings.ToLower(postLayer.PostRepo.postTableDef.TableName))
	postLayer.logger.Debug(queryDel)

	fields := postLayer.PostRepo.postTableDef.FieldMappings

	if len(fields) == 0 {
		postLayer.logger.Errorf("Please define all fields in config that is involved in dataset %s and query: %s", datasetName, query)
		return errors.New("fields needs to be defined in the configuration")
	}

	//Only Sort Fields if SortOrder is set
	count := 0
	for _, field := range fields {
		if field.SortOrder == 0 {
			count++
		}
	}
	if count >= 2 {
		postLayer.logger.Warn("No sort order is defined for fields in config, this might corrupt the query")
	} else {
		sort.SliceStable(fields, func(i, j int) bool {
			return fields[i].SortOrder < fields[j].SortOrder
		})
	}

	//TODO: Do in Batch Somehow
	tx, err := postLayer.PostRepo.DB.Begin()
	if err != nil {
		return err
	}

	for _, entity := range entities {
		s := entity.StripProps()

		args := make([]interface{}, len(fields))


		for i, field := range fields {
			args[i] = s[field.FieldName]
		}

		if !entity.IsDeleted { //If is deleted False --> Do not store
			_, err = tx.ExecContext(context.Background(), query, args...)
			if err != nil {
				_ = tx.Rollback()
				postLayer.logger.Error(err)
				return err
			}
		} else { //Should be deleted if it exists
			_, err := tx.Exec(queryDel, args[0])
			if err != nil {
				_ = tx.Rollback()
				postLayer.logger.Error(err)
				return err
			}
		}

	}
	_ = tx.Commit()

	return nil
}

func (postLayer *PostLayer) GetTableDefinition(datasetName string) *conf.PostMapping {
	for _, table := range postLayer.cmgr.Datalayer.PostMappings {
		if table.DatasetName == datasetName {
			return table
		} else if table.TableName == datasetName { // fallback
			return table
		}
	}
	return nil
}

func (postLayer *PostLayer) ensureConnection() error {
	postLayer.logger.Debug("Ensuring connection")
	if postLayer.cmgr.State.Digest != postLayer.PostRepo.digest {
		postLayer.logger.Debug("Configuration has changed need to reset connection")
		if postLayer.PostRepo.DB != nil {
			postLayer.PostRepo.DB.Close() // don't really care about the error, as long as it is closed
		}
		db, err := postLayer.connect() // errors are already logged
		if err != nil {
			return err
		}
		postLayer.PostRepo.DB = db
		postLayer.PostRepo.digest = postLayer.cmgr.State.Digest
	}
	return nil
}
