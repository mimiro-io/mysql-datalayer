package layer

import (
	"context"
	common "github.com/mimiro-io/common-datalayer"
	"os"
	"sort"
)

type MysqlDatalayer struct {
	db       *MysqlDB
	datasets map[string]*Dataset
	config   *common.Config
	logger   common.Logger
	metrics  common.Metrics
}

type Dataset struct {
	logger            common.Logger
	db                *MysqlDB
	datasetDefinition *common.DatasetDefinition
}

func (d *Dataset) MetaData() map[string]any {
	return d.datasetDefinition.SourceConfig
}

func (d *Dataset) Name() string {
	return d.datasetDefinition.DatasetName
}

func (dl *MysqlDatalayer) Stop(ctx context.Context) error {
	err := dl.db.db.Close()
	if err != nil {
		return err
	}

	return nil
}

func (dl *MysqlDatalayer) Dataset(dataset string) (common.Dataset, common.LayerError) {
	ds, found := dl.datasets[dataset]
	if found {
		return ds, nil
	}
	return nil, ErrDatasetNotFound(dataset)
}

func (dl *MysqlDatalayer) DatasetDescriptions() []*common.DatasetDescription {
	var datasetDescriptions []*common.DatasetDescription
	for key := range dl.datasets {
		datasetDescriptions = append(datasetDescriptions, &common.DatasetDescription{Name: key})
	}
	sort.Slice(datasetDescriptions, func(i, j int) bool {
		return datasetDescriptions[i].Name < datasetDescriptions[j].Name
	})
	return datasetDescriptions
}

func NewMysqlDataLayer(conf *common.Config, logger common.Logger, metrics common.Metrics) (common.DataLayerService, error) {
	mysqldb, err := newMysqlDB(conf)
	if err != nil {
		return nil, err
	}
	l := &MysqlDatalayer{
		datasets: map[string]*Dataset{},
		logger:   logger,
		metrics:  metrics,
		config:   conf,
		db:       mysqldb,
	}
	err = l.UpdateConfiguration(conf)
	if err != nil {
		return nil, err
	}
	return l, nil
}

func EnrichConfig(config *common.Config) error {
	// read env for user and password
	user := os.Getenv("MYSQL_USER")
	password := os.Getenv("MYSQL_PASSWORD")
	database := os.Getenv("MYSQL_DATABASE")
	host := os.Getenv("MYSQL_HOST")
	port := os.Getenv("MYSQL_PORT")

	if user != "" {
		config.NativeSystemConfig["user"] = user
	}

	if password != "" {
		config.NativeSystemConfig["password"] = password
	}

	if database != "" {
		config.NativeSystemConfig["database"] = database
	}

	if host != "" {
		config.NativeSystemConfig["host"] = host
	}

	if port != "" {
		config.NativeSystemConfig["port"] = port
	}

	return nil
}
