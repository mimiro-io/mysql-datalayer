package security

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gojektech/heimdall/v6/httpclient"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// Auth0Config contains the auth0 configuration
type Auth0Config struct {
	ClientId     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Audience     string `json:"audience"`
	GrantType    string `json:"grant_type"`
	endpoint     string
	logger       *zap.SugaredLogger
	cache        *cache
}

type cache struct {
	until time.Time
	token string
}

type auth0Response struct {
	AccessToken string `json:"access_token"`
	Scope       string `json:"scope"`
	ExpiresIn   int64  `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// NewAuth0Config creates a new Auth0Config struct, populated with the values from Viper.
func NewAuth0Config(logger *zap.SugaredLogger) *Auth0Config {
	// todo: we embed the use of viper here, but should in reality want it injected differently?
	clientId := viper.GetString("AUTH0_CLIENT_ID")
	if clientId == "" { // we assume it is not configured, so we leave it alone
		return nil
	}

	config := &Auth0Config{
		ClientId:     clientId,
		ClientSecret: viper.GetString("AUTH0_CLIENT_SECRET"),
		Audience:     viper.GetString("AUTH0_AUDIENCE"),
		GrantType:    viper.GetString("AUTH0_GRANT_TYPE"),
		endpoint:     viper.GetString("AUTH0_ENDPOINT"),
		logger:       logger.Named("auth0"),
	}
	return config
}

// Token returns a valid token, or an error caused when getting one
// Successive calls to this will return cached values, where the cache
// validity equals the token validity. Experience-wise, this can lead to
// race conditions when the caller asks for a valid token that is about to
// run out, and then it runs out before it can be used.
// If the cache is invalid, there is also no protection against cache stampedes,
// so if many calls are calling at the same time, they will hit Auth0 where the can
// be rate limited.
func (auth0 *Auth0Config) Token() (string, error) {
	token, err := auth0.generateOrGetToken()
	if err != nil {
		auth0.logger.Warnf("Error getting token: %w", err)
		return "", err
	}
	return fmt.Sprintf("Bearer %s", token), nil
}

func (auth0 *Auth0Config) generateOrGetToken() (string, error) {
	now := time.Now()
	if auth0.cache == nil || now.After(auth0.cache.until) {
		// first run
		res, err := auth0.callRemote()
		if err != nil {
			return "", err
		}
		auth0.cache = &cache{
			until: time.Now().Add(time.Duration(res.ExpiresIn) * time.Second),
			token: res.AccessToken,
		}
	}

	return auth0.cache.token, nil
}

func (auth0 *Auth0Config) callRemote() (*auth0Response, error) {
	timeout := 1000 * time.Millisecond
	client := httpclient.NewClient(httpclient.WithHTTPTimeout(timeout))

	auth0.logger.Infof("Got: %s, %s, %s", auth0.ClientId, auth0.Audience, auth0.GrantType)

	requestBody, err := json.Marshal(map[string]string{
		"client_id":     auth0.ClientId,
		"client_secret": auth0.ClientSecret,
		"audience":      auth0.Audience,
		"grant_type":    auth0.GrantType,
	})
	if err != nil {
		auth0.logger.Warn(err)
		return nil, err
	}

	req, err := http.NewRequest("POST", auth0.endpoint, bytes.NewBuffer(requestBody))
	if err != nil {
		auth0.logger.Warn(err)
		return nil, err
	}
	req.Header.Add("Content-Type", "application/json")

	auth0.logger.Debugf("Calling auth0: %s", auth0.endpoint)
	res, err := client.Do(req)
	if err != nil {
		auth0.logger.Warn(err)
		return nil, err
	}

	auth0Response := &auth0Response{}
	err = json.NewDecoder(res.Body).Decode(auth0Response)
	if err != nil {
		return nil, err
	}
	return auth0Response, nil
}
