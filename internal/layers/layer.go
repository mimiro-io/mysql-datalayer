package layers

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	_ "github.com/go-sql-driver/mysql"

	"github.com/mimiro-io/mysql-datalayer/internal/conf"
	"github.com/mimiro-io/mysql-datalayer/internal/db"
	"go.uber.org/fx"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Layer struct {
	cmgr   *conf.ConfigurationManager
	logger *zap.SugaredLogger
	Repo   *Repository //exported because it needs to deferred from main
}

type Repository struct {
	DB       *sql.DB
	ctx      context.Context
	tableDef *conf.TableMapping
	digest   [16]byte
}

type DatasetRequest struct {
	DatasetName string
	Since       string
	Limit       int64
}

const jsonNull = "null"

func NewLayer(lc fx.Lifecycle, cmgr *conf.ConfigurationManager, env *conf.Env) *Layer {
	layer := &Layer{}
	layer.cmgr = cmgr
	layer.logger = env.Logger.Named("layer")
	layer.Repo = &Repository{
		ctx: context.Background(),
	}
	_ = layer.ensureConnection() // ok with error here

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			if layer.Repo.DB != nil {
				layer.Repo.DB.Close()
			}
			return nil
		},
	})

	return layer
}

func (l *Layer) GetDatasetPostNames() []string {
	names := make([]string, 0)
	for _, table := range l.cmgr.Datalayer.PostMappings {
		names = append(names, table.DatasetName)
	}
	return names
}
func (l *Layer) GetDatasetNames() []string {
	names := make([]string, 0)
	for _, table := range l.cmgr.Datalayer.TableMappings {
		names = append(names, table.TableName)
	}
	return names
}

func (l *Layer) GetTableDefinition(datasetName string) *conf.TableMapping {
	for _, table := range l.cmgr.Datalayer.TableMappings {
		if table.TableName == datasetName {
			return table
		}
	}
	return nil
}

func (l *Layer) GetContext(datasetName string) map[string]interface{} {
	tableDef := l.GetTableDefinition(datasetName)
	ctx := make(map[string]interface{})
	namespaces := make(map[string]string)

	namespace := tableDef.TableName
	if tableDef.NameSpace != "" {
		namespace = tableDef.NameSpace
	}

	namespaces["ns0"] = l.cmgr.Datalayer.BaseNameSpace + namespace + "/"
	namespaces["rdf"] = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	ctx["namespaces"] = namespaces
	ctx["id"] = "@context"
	return ctx
}

func (l *Layer) DoesDatasetExist(datasetName string) bool {
	names := l.GetDatasetNames()
	for _, name := range names {
		if name == datasetName {
			return true
		}
	}
	return false
}

func (l *Layer) ChangeSet(request db.DatasetRequest, callBack func(*Entity)) error {

	tableDef := l.GetTableDefinition(request.DatasetName)
	if tableDef == nil {
		l.er(fmt.Errorf("could not find defined dataset: %s", request.DatasetName))
		return nil
	}

	err := l.ensureConnection()
	if err != nil {
		return err
	}

	query := db.NewQuery(request, tableDef, l.cmgr.Datalayer)

	var rows *sql.Rows

	since, err := serverSince(l.Repo.DB)
	if err != nil {
		l.er(err)
		return err
	}

	//rows, err = l.Repo.DB.Query( query.BuildQuery())
	rows, err = l.Repo.DB.QueryContext(l.Repo.ctx, query.BuildQuery())
	defer func() {
		rows.Close()
	}()

	if err != nil {
		l.er(err)
		return err
	}

	cols, err := rows.Columns()
	colTypes, _ := rows.ColumnTypes()

	// set up the row interface from the returned types
	nullableRowData := buildRowType(cols, colTypes)

	for rows.Next() {
		err = rows.Scan(nullableRowData...)

		if err != nil {
			l.er(err)
		} else {
			entity := l.toEntity(nullableRowData, cols, colTypes, tableDef)

			if entity != nil {
				// add types to entity
				if len(tableDef.Types) == 1 {
					entity.References["rdf:type"] = tableDef.Types[0]
				} else if len(tableDef.Types) > 1 {
					// multiple types...
					// fix me
				}

				// call back function
				callBack(entity)
			}
		}

	}

	// only add continuation token if enabled
	if tableDef.CDCEnabled {
		entity := NewEntity()
		entity.ID = "@continuation"
		entity.Properties["token"] = since

		callBack(entity)
	}

	if err := rows.Err(); err != nil {
		l.er(err)
		return nil // this is already at the end, we don't care about this error now
	}

	// clean it up
	return nil
}

type HexNullBool bool

func (b *HexNullBool) Scan(src interface{}) error {
	str, ok := src.([]byte)
	if !ok {
		v := false
		*b = HexNullBool(v)
	}
	hexString := hex.EncodeToString(str)
	switch hexString {
	case "00":
		v := false
		*b = HexNullBool(v)
	case "01":
		v := true
		*b = HexNullBool(v)
	}
	return nil
}

func buildRowType(cols []string, colTypes []*sql.ColumnType) []interface{} {
	nullableRowData := make([]interface{}, len(cols))
	for i := range cols {
		colDef := colTypes[i]
		ctType := colDef.DatabaseTypeName()

		switch ctType {
		case "INT", "SMALLINT", "TINYINT":
			nullableRowData[i] = new(sql.NullInt64)
		case "VARCHAR", "NVARCHAR", "TEXT", "NTEXT":
			nullableRowData[i] = new(sql.NullString)
		case "DATETIME", "DATE", "TIMESTAMP":
			nullableRowData[i] = new(sql.NullTime)
		case "MONEY", "DECIMAL", "FLOAT", "DOUBLE":
			nullableRowData[i] = new(sql.NullFloat64)
		case "BIT":
			nullableRowData[i] = new(HexNullBool)
		default:
			nullableRowData[i] = new(sql.RawBytes)
		}
	}
	return nullableRowData
}

func (l *Layer) er(err error) {
	l.logger.Warnf("Got error %s", err)
}

func (l *Layer) ensureConnection() error {
	l.logger.Debug("Ensuring connection")
	if l.cmgr.State.Digest != l.Repo.digest {
		l.logger.Debug("Configuration has changed need to reset connection")
		if l.Repo.DB != nil {
			l.Repo.DB.Close() // don't really care about the error, as long as it is closed
		}
		db, err := l.connect() // errors are already logged
		if err != nil {
			return err
		}
		l.Repo.DB = db
		l.Repo.digest = l.cmgr.State.Digest
	}
	return nil
}

func (l *Layer) connect() (*sql.DB, error) {

	u := &url.URL{
		Scheme: "mysql",
		User:   url.UserPassword(l.cmgr.Datalayer.User, l.cmgr.Datalayer.Password),
		Host:   fmt.Sprintf("%s:%s", l.cmgr.Datalayer.DatabaseServer, l.cmgr.Datalayer.Port),
		Path:   l.cmgr.Datalayer.Database,
	}

	//fmt.Println(fmt.Sprintf("%v:%v@(%v)/%v?%v", u.User.Username(), l.cmgr.Datalayer.Password, u.Host, u.Path, "parseTime=true&loc=&loc=Europe%2FStockholm"))
	db, err := sql.Open(u.Scheme, fmt.Sprintf("%v:%v@(%v)/%v?%s", u.User.Username(), l.cmgr.Datalayer.Password, u.Host, u.Path, "parseTime=true"))
	if err != nil {
		l.logger.Warn("Error creating connection pool: ", err.Error())
		return nil, err
	}

	err = db.PingContext(l.Repo.ctx)
	if err != nil {
		l.logger.Warn(err.Error())
		return nil, err
	}
	return db, nil
}

// mapColumns remaps the ColumnMapping into Column
func mapColumns(columns []*conf.ColumnMapping) map[string]*conf.ColumnMapping {
	cms := make(map[string]*conf.ColumnMapping)

	for _, cm := range columns {
		cms[cm.FieldName] = cm
	}
	return cms

}

func (l *Layer) toEntity(rowType []interface{}, cols []string, colTypes []*sql.ColumnType, tableDef *conf.TableMapping) *Entity {
	entity := NewEntity()

	for i, raw := range rowType {
		if raw != nil {
			ct := colTypes[i]
			ctName := ct.DatabaseTypeName()
			colName := cols[i]
			colMapping := tableDef.Columns[colName]
			colName = "ns0:" + colName

			var val interface{} = nil
			var strVal = ""

			if colName == "ns0:__$operation" {
				ptrToNullInt := raw.(*sql.NullInt64)
				if (*ptrToNullInt).Valid {
					operation := (*ptrToNullInt).Int64
					if operation == 1 {
						entity.IsDeleted = true
					}
				}
			}

			if colMapping != nil {
				if colMapping.IgnoreColumn {
					continue
				}

				if colMapping.PropertyName != "" {
					colName = colMapping.PropertyName
				}
			} else {
				// filter out cdc columns
				if ignoreColumn(cols[i], tableDef) {
					continue
				}
			}

			entity.Properties[colName] = nil

			switch ctName {
			case "VARCHAR", "NVARCHAR", "TEXT", "NTEXT":
				ptrToNullString := raw.(*sql.NullString)
				if (*ptrToNullString).Valid {
					val = (*ptrToNullString).String
					strVal = val.(string)
					entity.Properties[colName] = val
				}
			case "UNIQUEIDENTIFIER":
				ptrToString := raw.(*sql.RawBytes)
				if (*ptrToString) != nil {
					uid, _ := uuid.FromBytes(*ptrToString)
					val = uid.String()
					entity.Properties[colName] = val
				}
			case "DATETIME", "DATE", "TIMESTAMP":
				ptrToNullDatetime := raw.(*sql.NullTime)
				if (*ptrToNullDatetime).Valid {
					val = (*ptrToNullDatetime).Time
					entity.Properties[colName] = val
				}
			case "INT", "SMALLINT", "TINYINT":
				ptrToNullInt := raw.(*sql.NullInt64)
				if (*ptrToNullInt).Valid {
					val = (*ptrToNullInt).Int64
					strVal = strconv.FormatInt((*ptrToNullInt).Int64, 10)
					entity.Properties[colName] = val
				}
			case "BIGINT":
				ptrToSomething := raw.(*sql.RawBytes)
				if *ptrToSomething != nil {
					val, err := toInt64(*ptrToSomething)
					if err != nil {
						l.logger.Warnf("Error converting to int64: %v", err)
					} else {
						strVal = strconv.FormatInt(val, 10)
						entity.Properties[colName] = val
					}
				}
			case "MONEY", "DECIMAL", "FLOAT", "DOUBLE":
				ptrToNullFloat := raw.(*sql.NullFloat64)
				if (*ptrToNullFloat).Valid {
					entity.Properties[colName] = (*ptrToNullFloat).Float64
				}
			case "BIT":
				ptrToNullBool := raw.(*HexNullBool)
				entity.Properties[colName] = ptrToNullBool
			default:
				l.logger.Infof("Got: %s for %s", ctName, colName)
			}

			if colMapping != nil {
				// is this the id column
				if colMapping.IsIdColumn && strVal != "" {
					entity.ID = l.cmgr.Datalayer.BaseUri + fmt.Sprintf(tableDef.EntityIdConstructor, strVal)
				}

				if colMapping.IsReference && strVal != "" {
					entity.References[colName] = fmt.Sprintf(colMapping.ReferenceTemplate, strVal)
				}
			}
		}
	}

	if entity.IsDeleted {
		entity.Properties = make(map[string]interface{})
		entity.References = make(map[string]interface{})
	}
	if entity.ID == "" { // this is invalid
		return nil
	}

	return entity
}

// serverSince queries the server for its time, this will be used as the source of the since to return
// when using cdc. The return value is Base64 encoded
func serverSince(db *sql.DB) (string, error) {
	var dt sql.NullTime

	err := db.QueryRowContext(context.Background(), "select current_timestamp").Scan(&dt)
	if err != nil {
		return "", err
	}

	s := fmt.Sprintf("%s", dt.Time.Format(time.RFC3339))
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}

func toInt64(payload sql.RawBytes) (int64, error) {
	content := reflect.ValueOf(payload).Interface().(sql.RawBytes)
	data := string(content)                  //convert to string
	i, err := strconv.ParseInt(data, 10, 64) // convert to int or your preferred data type
	if err != nil {
		return 0, err
	}
	return i, nil
}

func ignoreColumn(column string, tableDef *conf.TableMapping) bool {

	if tableDef.CDCEnabled && strings.HasPrefix(column, "__$") {
		return true
	}
	return false
}
