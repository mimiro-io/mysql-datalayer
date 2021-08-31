package conf

import (
	"github.com/DataDog/datadog-go/statsd"
	"github.com/spf13/viper"
)

func NewStatsd(env *Env) (statsd.ClientInterface, error) {
	var client statsd.ClientInterface
	agentEndpoint := viper.GetViper().GetString("DD_AGENT_HOST")
	service := viper.GetViper().GetString("SERVICE_NAME")
	if agentEndpoint != "" {
		opt := statsd.WithNamespace(service)
		env.Logger.Info("Statsd is configured on: ", viper.GetViper().GetString("DD_AGENT_HOST"))
		c, err := statsd.New(viper.GetViper().GetString("DD_AGENT_HOST"), opt)
		c.Tags = []string{
			env.ServiceName,
		}
		if err != nil {
			return nil, err
		}
		client = c
	} else {
		env.Logger.Debug("Using NoOp statsd client")
		client = &statsd.NoOpClient{}
	}

	return client, nil
}
