package conf

import (
	"go.uber.org/zap"
)

type Env struct {
	Logger          *zap.SugaredLogger
	Env             string
	Port            string
	ConfigLocation  string
	RefreshInterval string
	ServiceName     string
	User            User
	Auth            *AuthConfig
}

type User struct {
	UserName string
	Password string
}

type AuthConfig struct {
	WellKnown     string
	Audience      string
	AudienceAuth0 string
	Issuer        string
	IssuerAuth0   string
	Middleware    string
}
