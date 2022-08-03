package middlewares

import (
	"fmt"
	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"strings"
	"time"
)

type LoggerConfig struct {
	// Skipper defines a function to skip middleware.
	Skipper middleware.Skipper

	// BeforeFunc defines a function which is executed just before the middleware.
	BeforeFunc middleware.BeforeFunc

	Logger *zap.Logger

	StatsdClient statsd.ClientInterface
}

func LoggerFilter(config LoggerConfig) echo.MiddlewareFunc {
	service := viper.GetViper().GetString("SERVICE_NAME")

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if config.Skipper(c) {
				return next(c)
			}

			if config.BeforeFunc != nil {
				config.BeforeFunc(c)
			}

			start := time.Now()
			req := c.Request()
			res := c.Response()

			tags := []string{
				fmt.Sprintf("application:%s", service),
				fmt.Sprintf("method:%s", strings.ToLower(req.Method)),
				fmt.Sprintf("url:%s", strings.ToLower(req.RequestURI)),
				fmt.Sprintf("status:%d", res.Status),
			}

			err := next(c)
			if err != nil {
				c.Error(err)
			}

			timed := time.Since(start)

			err = config.StatsdClient.Incr("http.count", tags, 1)
			err = config.StatsdClient.Timing("http.time", timed, tags, 1)
			err = config.StatsdClient.Gauge("http.size", float64(res.Size), tags, 1)
			if err != nil {
				config.Logger.Warn("Error with statsd", zap.String("error", fmt.Sprintf("%s", err)))
			}

			msg := fmt.Sprintf("%d - %s %s (time: %s, size: %d, user_agent: %s)", res.Status, req.Method, req.RequestURI, timed.String(), res.Size, req.UserAgent())

			fields := []zapcore.Field{
				zap.String("time", timed.String()),
				zap.String("request", fmt.Sprintf("%s %s", req.Method, req.RequestURI)),
				zap.Int("status", res.Status),
				zap.Int64("size", res.Size),
				zap.String("user_agent", req.UserAgent()),
			}

			id := req.Header.Get(echo.HeaderXRequestID)
			if id == "" {
				id = res.Header().Get(echo.HeaderXRequestID)
				fields = append(fields, zap.String("request_id", id))
			}

			config.Logger.Info(msg)

			return nil
		}
	}
}
