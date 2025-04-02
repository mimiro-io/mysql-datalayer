package db

import (
	"encoding/base64"
	"fmt"
	"github.com/mimiro-io/mysql-datalayer/internal/legacy/conf"

	"time"
)

type DatasetRequest struct {
	DatasetName string
	Since       string
	Limit       int64
}

type TableQuery interface {
	BuildQuery() string
}

type FullQuery struct {
	Datalayer *conf.Datalayer
	Request   DatasetRequest
	TableDef  *conf.TableMapping
}

func NewQuery(request DatasetRequest, tableDef *conf.TableMapping, datalayer *conf.Datalayer) TableQuery {
	if tableDef.CDCEnabled && request.Since != "" {
		return CDCQuery{
			Datalayer: datalayer,
			Request:   request,
			TableDef:  tableDef,
		}
	} else {
		return FullQuery{
			Datalayer: datalayer,
			Request:   request,
			TableDef:  tableDef,
		}
	}
}

func (q FullQuery) BuildQuery() string {
	limit := ""
	if q.Request.Limit > 0 {
		limit = fmt.Sprintf(" limit %d ", q.Request.Limit)
	}
	query := fmt.Sprintf("select * from %s%s;", q.TableDef.TableName, limit)
	if q.TableDef.CustomQuery != "" {
		query = fmt.Sprintf(q.TableDef.CustomQuery, limit)
	}

	return query
}

type CDCQuery struct {
	Datalayer *conf.Datalayer
	Request   DatasetRequest
	TableDef  *conf.TableMapping
}

func (q CDCQuery) BuildQuery() string {
	date := "GETDATE()-1"
	data, err := base64.StdEncoding.DecodeString(q.Request.Since)
	if err == nil {
		dt, _ := time.Parse(time.RFC3339, string(data))
		date = fmt.Sprintf("DATETIMEFROMPARTS( %d, %d, %d, %d, %d, %d, 0)",
			dt.Year(), dt.Month(), dt.Day(), dt.Hour(), dt.Minute(), dt.Second())
	}
	query := fmt.Sprintf(`
		DECLARE @begin_time DATETIME, @end_time DATETIME, @begin_lsn BINARY(10), @end_lsn BINARY(10);
		SELECT @begin_time = %s, @end_time = GETDATE();
		SELECT @begin_lsn = sys.fn_cdc_map_time_to_lsn('smallest greater than', @begin_time);
		SELECT @end_lsn = sys.fn_cdc_map_time_to_lsn('largest less than or equal', @end_time);
		IF @begin_lsn IS NOT NULL BEGIN
			SELECT * FROM [cdc].[fn_cdc_get_all_changes_dbo_%s](@begin_lsn,@end_lsn,N'ALL');
		END
	`, date, q.TableDef.TableName)

	return query
}
