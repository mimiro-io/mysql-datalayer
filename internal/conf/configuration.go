package conf

import (
	"fmt"
	"net/url"
	"os"
)

type Datalayer struct {
	Id             string          `json:"id"`
	DatabaseServer string          `json:"databaseServer"`
	BaseUri        string          `json:"baseUri"`
	Database       string          `json:"database"`
	Port           string          `json:"port"`
	Schema         string          `json:"schema"`
	BaseNameSpace  string          `json:"baseNameSpace"`
	User           string          `json:"user"`
	Password       string          `json:"password"`
	TableMappings  []*TableMapping `json:"tableMappings"`
	PostMappings   []*PostMapping  `json:"postMappings"`
}

type TableMapping struct {
	TableName           string           `json:"tableName"`
	NameSpace           string           `json:"nameSpace"`
	CustomQuery         string           `json:"query"`
	CDCEnabled          bool             `json:"cdcEnabled"`
	EntityIdConstructor string           `json:"entityIdConstructor"`
	Types               []string         `json:"types"`
	ColumnMappings      []*ColumnMapping `json:"columnMappings"`
	Columns             map[string]*ColumnMapping
}

type ColumnMapping struct {
	FieldName         string           `json:"fieldName"`
	PropertyName      string           `json:"propertyName"`
	IsIdColumn        bool             `json:"isIdColumn"`
	IsReference       bool             `json:"isReference"`
	IsEntity          bool             `json:"isEntity"`
	ReferenceTemplate string           `json:"referenceTemplate"`
	IgnoreColumn      bool             `json:"ignoreColumn"`
	IdTemplate        string           `json:"idTemplate"`
	ColumnMappings    []*ColumnMapping `json:"columnMappings"`
}

type PostMapping struct {
	DatasetName   string          `json:"datasetName"`
	TableName     string          `json:"tableName"`
	Query         string          `json:"query"`
	Config        *TableConfig    `json:"config"`
	FieldMappings []*FieldMapping `json:"fieldMappings"`
}

type TableConfig struct {
	DatabaseServer *string         `json:"databaseServer"`
	Database       *string         `json:"database"`
	Port           *string         `json:"port"`
	Schema         *string         `json:"schema"`
	User           *VariableGetter `json:"user"`
	Password       *VariableGetter `json:"password"`
}

type FieldMapping struct {
	FieldName       string `json:"fieldName"`
	ToMySQLField string `json:"toMySQLField"`
	SortOrder       int    `json:"order"`
	Type            string `json:"type"`
}

type VariableGetter struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

func (v *VariableGetter) GetValue() string {
	switch v.Type {
	case "direct":
		return v.Key
	default:
		return os.Getenv(v.Key)
	}
}

func (layer *Datalayer) GetUrl(mapping *PostMapping) *url.URL {
	database := layer.Database
	port := layer.Port
	server := layer.DatabaseServer
	user := layer.User
	password := layer.Password
	scheme := layer.Schema
	if scheme == "" {
		scheme = "mysql"
	}

	if mapping.Config != nil {
		if mapping.Config.Schema != nil {
			scheme = *mapping.Config.Schema
		}
		if mapping.Config.Database != nil {
			database = *mapping.Config.Database
		}
		if mapping.Config.Port != nil {
			port = *mapping.Config.Port
		}
		if mapping.Config.DatabaseServer != nil {
			server = *mapping.Config.DatabaseServer
		}
		if mapping.Config.User != nil {
			user = mapping.Config.User.GetValue()
		}
		if mapping.Config.Password != nil {
			password = mapping.Config.Password.GetValue()
		}
	}

	u := &url.URL{
		Scheme: scheme,
		User:   url.UserPassword(user, password),
		Host:   fmt.Sprintf("%s:%s", server, port),
		Path:   database,
	}

	return u
}
