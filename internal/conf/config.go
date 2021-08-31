package conf

import (
	"fmt"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
)

func NewEnv() *Env {
	profile, found := os.LookupEnv("PROFILE")
	if !found {
		profile = "local"
	}

	service, _ := os.LookupEnv("SERVICE_NAME")
	logger := GetLogger(profile, zapcore.InfoLevel, service) // add a default logger while loading the env
	logger.Infof("Loading env: %s", profile)

	parseEnv(profile, logger)

	logger.Infof("Config location: %s", viper.GetString("CONFIG_LOCATION"))

	return &Env{
		Logger:          logger,
		Env:             profile,
		Port:            viper.GetString("SERVER_PORT"),
		ConfigLocation:  viper.GetString("CONFIG_LOCATION"),
		RefreshInterval: viper.GetString("CONFIG_REFRESH_INTERVAL"),
		ServiceName:     viper.GetString("SERVICE_NAME"),
		User: User{
			UserName: viper.GetString("MYSQL_DB_USER"),
			Password: viper.GetString("MYSQL_DB_PASSWORD"),
		},
		Auth: &AuthConfig{
			WellKnown:     viper.GetString("TOKEN_WELL_KNOWN"),
			Audience:      viper.GetString("TOKEN_AUDIENCE"),
			AudienceAuth0: viper.GetString("TOKEN_AUDIENCE_AUTH0"),
			Issuer:        viper.GetString("TOKEN_ISSUER"),
			IssuerAuth0:   viper.GetString("TOKEN_ISSUER_AUTH0"),
			Middleware:    viper.GetString("AUTHORIZATION_MIDDLEWARE"),
		},
	}
}

func parseEnv(env string, logger *zap.SugaredLogger) {
	viper.SetConfigType("env")
	viper.AddConfigPath(".")
	viper.SetDefault("SERVER_PORT", "8080")
	viper.SetDefault("LOG_LEVEL", "INFO")
	viper.SetDefault("CONFIG_REFRESH_INTERVAL", "@every 60s")
	viper.SetDefault("SERVICE_NAME", "mysql-datalayer")
	viper.AutomaticEnv()

	viper.SetDefault("CONFIG_LOCATION", fmt.Sprintf("file://%s", ".config.json"))

	// read the .env file first
	viper.SetConfigName(".env")
	err := viper.ReadInConfig() // Find and read the config file
	if err != nil {             // Handle errors reading the config file
		logger.DPanicf("Fatal error config file: %s", err)
		panic(fmt.Errorf("Fatal error config file: %s \n", err))
	}

	logger.Infof("Reading config file %s", viper.GetViper().ConfigFileUsed())

	viper.SetConfigName(fmt.Sprintf(".env-%s", env))
	err = viper.MergeInConfig()
	if err != nil {
		logger.Infof("Could not find .env-%s", env)
	} else {
		logger.Infof("Reading config file %s", viper.GetViper().ConfigFileUsed())
	}
}
