package layer

import (
	"database/sql"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	common "github.com/mimiro-io/common-datalayer"
)

type MysqlDB struct {
	db *sql.DB
}

func newMysqlDB(conf *common.Config) (*MysqlDB, error) {
	c, err := newMysqlConf(conf)
	if err != nil {
		return nil, err
	}
	connStr := fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?parseTime=true&multiStatements=true",
		c.User,
		c.Password,
		c.Hostname,
		c.Port,
		c.Database,
	)

	db, cerr := sql.Open("mysql", connStr)
	if cerr != nil {
		return nil, ErrConnection(cerr)
	}

	// Ping the database to verify DSN provided by the user.
	perr := db.Ping()
	if perr != nil {
		return nil, ErrConnection(perr)
	}

	return &MysqlDB{db}, nil
}

type RowItem struct {
	Map     map[string]any
	Columns []string
	Values  []any
	deleted bool
}

func (r *RowItem) GetValue(name string) any {
	val := r.Map[name]
	switch v := val.(type) {
	case *sql.NullBool:
		return v.Valid && v.Bool
	case *sql.NullString:
		if v.Valid {
			return v.String
		} else {
			return nil
		}
	case *sql.NullInt64:
		if v.Valid {
			return v.Int64
		} else {
			return nil
		}

	case *sql.NullFloat64:
		if v.Valid {
			if v.Float64 == float64(int64(v.Float64)) {
				return int64(v.Float64)
			} else {
				return v.Float64
			}

		} else {
			return nil
		}
	case *sql.NullTime:
		if v.Valid {
			return v.Time
		} else {
			return nil
		}
	case nil:
		return nil
	default:
		return "invalid type"
	}
}

func (r *RowItem) SetValue(name string, value any) {
	r.Columns = append(r.Columns, name)
	r.Values = append(r.Values, value)
	r.Map[name] = value
}

func (r *RowItem) NativeItem() any {
	return r.Map
}

func (r *RowItem) GetPropertyNames() []string {
	return r.Columns
}
