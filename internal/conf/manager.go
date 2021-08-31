package conf

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mimiro-io/mysql-datalayer/internal/security"

	"github.com/bamzi/jobrunner"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/gojektech/heimdall/v6/httpclient"
)

type ConfigurationManager struct {
	configLocation  string
	refreshInterval string
	Datalayer       *Datalayer
	logger          *zap.SugaredLogger
	State           State
	TokenProviders  *security.TokenProviders
	user            user
}

type State struct {
	Timestamp int64
	Digest    [16]byte
}

type user struct {
	userName string
	password string
}

func NewConfigurationManager(lc fx.Lifecycle, env *Env, providers *security.TokenProviders) *ConfigurationManager {
	config := &ConfigurationManager{
		configLocation:  env.ConfigLocation,
		refreshInterval: env.RefreshInterval,
		Datalayer:       &Datalayer{},
		TokenProviders:  providers,
		logger:          env.Logger.Named("configuration"),
		user: user{
			userName: env.User.UserName,
			password: env.User.Password,
		},
		State: State{
			Timestamp: time.Now().Unix(),
		},
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			config.Init()
			return nil
		},
	})
	return config
}

func (conf *ConfigurationManager) Init() {
	conf.logger.Infof("Starting the ConfigurationManager with refresh %s\n", conf.refreshInterval)
	conf.load()
	conf.logger.Info("Done loading the config")
	jobrunner.Start()
	err := jobrunner.Schedule(conf.refreshInterval, conf)
	if err != nil {
		conf.logger.Warn("Could not start configuration reload job")
	}
}

func (conf *ConfigurationManager) Run() {
	conf.load()
}

func (conf *ConfigurationManager) load() {
	var configContent []byte
	var err error
	if strings.Index(conf.configLocation, "file://") == 0 {
		configContent, err = conf.loadFile(conf.configLocation)
	} else if strings.Index(conf.configLocation, "http") == 0 {
		c, err := conf.loadUrl(conf.configLocation)
		if err != nil {
			conf.logger.Warn("Unable to parse json into config. Error is: "+err.Error()+". Please check file: "+conf.configLocation, err)
			return
		}
		configContent, err = unpackContent(c)
	} else {
		conf.logger.Errorf("Config file location not supported: %s \n", conf.configLocation)
		configContent, _ = conf.loadFile("file://resources/default-config.json")
	}
	if err != nil {
		// means no file found
		conf.logger.Infof("Could not find %s", conf.configLocation)
	}

	if configContent == nil {
		// again means not found or no content
		conf.logger.Infof("No values read for %s", conf.configLocation)
		configContent = make([]byte, 0)
	}

	state := State{
		Timestamp: time.Now().Unix(),
		Digest:    md5.Sum(configContent),
	}

	if state.Digest != conf.State.Digest {
		config, err := conf.parse(configContent)
		if err != nil {
			conf.logger.Warn("Unable to parse json into config. Error is: "+err.Error()+". Please check file: "+conf.configLocation, err)
			return
		}

		conf.Datalayer = conf.mapColumns(conf.setUser(config))
		conf.State = state
		conf.logger.Info("Updated configuration with new values")
	}

}

func (conf *ConfigurationManager) loadUrl(configEndpoint string) ([]byte, error) {
	timeout := 10000 * time.Millisecond
	client := httpclient.NewClient(httpclient.WithHTTPTimeout(timeout), httpclient.WithRetryCount(3))

	req, err := http.NewRequest("GET", configEndpoint, nil) //
	if err != nil {
		return nil, err
	}

	provider, ok := conf.TokenProviders.Providers["auth0tokenprovider"]
	if ok {
		tokenProvider := provider.(security.TokenProvider)
		bearer, err := tokenProvider.Token()
		if err != nil {
			conf.logger.Warnf("Token provider returned error: %w", err)
		}
		req.Header.Add("Authorization", bearer)
	}

	resp, err := client.Do(req)
	if err != nil {
		conf.logger.Error("Unable to open config url: "+configEndpoint, err)
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == 200 {
		return ioutil.ReadAll(resp.Body)
	} else {
		conf.logger.Infof("Endpoint returned %s", resp.Status)
		return nil, err
	}
}

type content struct {
	Id   string                 `json:"id"`
	Data map[string]interface{} `json:"data"`
}

func unpackContent(themBytes []byte) ([]byte, error) {
	unpacked := &content{}
	err := json.Unmarshal(themBytes, unpacked)
	if err != nil {
		return nil, err
	}
	data := unpacked.Data

	return json.Marshal(data)

}

func (conf *ConfigurationManager) loadFile(location string) ([]byte, error) {
	configFileName := strings.ReplaceAll(location, "file://", "")

	configFile, err := os.Open(configFileName)
	if err != nil {
		conf.logger.Error("Unable to open config file: "+configFileName, err)
		return nil, err
	}
	defer configFile.Close()
	return ioutil.ReadAll(configFile)
}

func (conf *ConfigurationManager) parse(config []byte) (*Datalayer, error) {
	configuration := &Datalayer{}
	err := json.Unmarshal(config, configuration)
	return configuration, err
}

// mapColumns remaps the ColumnMapping into Column
func (conf *ConfigurationManager) mapColumns(config *Datalayer) *Datalayer {
	for _, t := range config.TableMappings {
		columns := make(map[string]*ColumnMapping)

		for _, cm := range t.ColumnMappings {
			columns[cm.FieldName] = cm
		}
		t.Columns = columns
	}
	return config
}

func (conf *ConfigurationManager) setUser(config *Datalayer) *Datalayer {
	// set user name and password if not in config
	if config.User == "" {
		userName := conf.user.userName
		if userName == "" {
			conf.logger.Warn("No user name defined: set env var: MYSQL_DB_USER. Or include in config.json")
		}
		config.User = userName
		conf.logger.Debugf("Got user: %s", config.User)
	}

	if config.Password == "" {
		password := conf.user.password
		if password == "" {
			conf.logger.Warn("No password defined: set env var: MYSQL_DB_PASSWORD")
		}
		config.Password = password
	}
	return config
}
