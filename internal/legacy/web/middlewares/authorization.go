package middlewares

import (
	"github.com/golang-jwt/jwt"
	"github.com/juliangruber/go-intersect"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
	"net/http"
	"strings"
)

func Authorize(logger *zap.SugaredLogger, scopes ...string) echo.MiddlewareFunc {

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Get("user") == nil { // user never got set, oops
				return echo.NewHTTPError(http.StatusForbidden, "user not set")
			}
			token := c.Get("user").(*jwt.Token)

			// get the claims, and make sure nils are handled by checking for size
			claims := token.Claims.(*CustomClaims)
			if claims.Gty == "client-credentials" { // this is a machine or an application token
				var claimScopes []string
				if len(claims.scopes()) > 0 {
					claimScopes = strings.Split(claims.scopes()[0], " ")
				}
				res := intersect.Simple(claimScopes, scopes)
				if len(res) == 0 { // no intersection
					logger.Debugw("User attempted login with missing or wrong scope",
						"subject", token.Claims.(*CustomClaims).Subject,
						"scopes", claimScopes,
						"userScopes", scopes)
					return echo.NewHTTPError(http.StatusForbidden, "user attempted login with missing or wrong scope")

				}
			} else {
				// this is a user
				if !claims.Adm { // this will only be set for system admins, we only support mimiro Adm at the moment
					// if not, we need to see if the url requested contains the user id
					subject := claims.Subject
					// it needs the subject in the url
					uri := c.Request().RequestURI
					if strings.Index(uri, subject) == -1 { // not present, so forbidden
						return echo.NewHTTPError(http.StatusForbidden, "user has no access to path")
					}
				}
			}

			return next(c)
		}

	}

}

func NoOpAuthorizer(logger *zap.SugaredLogger, scopes ...string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			return next(c)
		}
	}
}
