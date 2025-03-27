package web

import (
	"context"
	"encoding/json"
	"github.com/labstack/echo/v4"
	"github.com/mimiro-io/mysql-datalayer/internal/legacy/db"
	layers2 "github.com/mimiro-io/mysql-datalayer/internal/legacy/layers"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

type ServiceInfo struct {
	Name     string
	Location string
}

type DatasetName struct {
	Name string   `json:"name"`
	Type []string `json:"type"`
}

type datasetHandler struct {
	logger *zap.SugaredLogger
	layer  *layers2.Layer
}

func NewDatasetHandler(lc fx.Lifecycle, e *echo.Echo, logger *zap.SugaredLogger, mw *Middleware, layer *layers2.Layer) {
	log := logger.Named("web")

	dh := &datasetHandler{
		logger: log,
		layer:  layer,
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			e.GET("/datasets", dh.listDatasetsHandler, mw.authorizer(log, "datahub:r"))
			e.GET("/datasets/:dataset/changes", dh.getChangesHandler, mw.authorizer(log, "datahub:r"))
			e.GET("/datasets/:dataset/entities", dh.getChangesHandler, mw.authorizer(log, "datahub:r"))
			return nil
		},
	})

}

// Handlers

// listDatasetsHandler
func (handler *datasetHandler) listDatasetsHandler(c echo.Context) error {
	names := make([]DatasetName, 0)
	datasets := handler.layer.GetDatasetNames()
	postDatasets := handler.layer.GetDatasetPostNames()
	sort.Strings(datasets)
	for _, v := range datasets {
		names = append(names, DatasetName{Name: v, Type: []string{"GET"}})
	}
	sort.Strings(postDatasets)
	for _, v := range postDatasets {
		names = append(names, DatasetName{Name: v, Type: []string{"POST"}})
	}

	return c.JSON(http.StatusOK, names)
}

// getEntitiesHandler
// path param dataset
// query param continuationToken
func (handler *datasetHandler) getEntitiesHandler(c echo.Context) error {
	datasetName, err := url.QueryUnescape(c.Param("dataset"))
	if err != nil {
		return c.NoContent(http.StatusBadRequest)
	}
	limit := c.QueryParam("limit")
	var l int64
	if limit != "" {
		f, _ := strconv.ParseInt(limit, 10, 64)
		l = f
	}

	// check dataset exists
	if !handler.layer.DoesDatasetExist(datasetName) {
		return c.NoContent(http.StatusNotFound)
	}

	c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c.Response().WriteHeader(http.StatusOK)
	enc := json.NewEncoder(c.Response())
	c.Response().Write([]byte("["))

	// make and send context as the first object
	context := handler.layer.GetContext(datasetName)

	_ = enc.Encode(context)

	request := db.DatasetRequest{
		DatasetName: datasetName,
		Limit:       l,
	}
	handler.layer.ChangeSet(request, func(entity *layers2.Entity) {
		c.Response().Write([]byte(","))
		_ = enc.Encode(entity)
		c.Response().Flush()
	})

	c.Response().Write([]byte("]"))
	c.Response().Flush()
	return nil
}

func (handler *datasetHandler) getChangesHandler(c echo.Context) error {
	datasetName, err := url.QueryUnescape(c.Param("dataset"))
	if err != nil {
		handler.logger.Warn(err)
		return c.NoContent(http.StatusBadRequest)
	}

	since := c.QueryParam("since")
	limit := c.QueryParam("limit")
	var l int64
	if limit != "" {
		f, _ := strconv.ParseInt(limit, 10, 64)
		l = f
	}

	// check dataset exists
	if !handler.layer.DoesDatasetExist(datasetName) {
		return c.NoContent(http.StatusNotFound)
	}

	c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c.Response().WriteHeader(http.StatusOK)
	enc := json.NewEncoder(c.Response())

	c.Response().Write([]byte("["))

	// make and send context as the first object
	context := handler.layer.GetContext(datasetName)

	_ = enc.Encode(context)

	request := db.DatasetRequest{
		DatasetName: datasetName,
		Since:       since,
		Limit:       l,
	}

	err = handler.layer.ChangeSet(request, func(entity *layers2.Entity) {
		if entity.ID == "@continuation" { // it is returned as a normal entity, and we need to flatten it to the token format
			cont := map[string]interface{}{
				"id":    "@continuation",
				"token": entity.Properties["token"],
			}
			c.Response().Write([]byte(","))
			_ = enc.Encode(cont)
			c.Response().Flush()
		} else {
			c.Response().Write([]byte(","))
			_ = enc.Encode(entity)
			c.Response().Flush()
		}
	})

	if err != nil {
		handler.logger.Warn(err)
	}

	/*entity := map[string]interface{}{
		"id":    "@continuation",
		"token": base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%o", time.Now().Unix()))),
	}

	c.Response().Write([]byte(","))
	_ = enc.Encode(entity)*/
	c.Response().Write([]byte("]"))
	c.Response().Flush()
	return nil

}
