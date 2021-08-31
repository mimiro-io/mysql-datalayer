package security

import "go.uber.org/zap"

type TokenProvider interface {
	Token() (string, error)
}

type TokenProviders struct {
	Providers map[string]interface{}
}

// NewTokenProviders provides a map of token providers, keyed on the
// name of the token provider struct as lower_case.
func NewTokenProviders(logger *zap.SugaredLogger) *TokenProviders {
	var providers = make(map[string]interface{}, 0)
	auth0 := NewAuth0Config(logger)
	if auth0 != nil {
		providers["auth0tokenprovider"] = auth0
	}

	return &TokenProviders{
		Providers: providers,
	}

}

func NoOpTokenProviders() *TokenProviders {
	var providers = make(map[string]interface{}, 0)
	return &TokenProviders{Providers: providers}
}
