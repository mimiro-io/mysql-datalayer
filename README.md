# MySQL Data Layer

A Data Layer for MySQL (https://www.mysql.com/) that conforms to the Universal Data API specification (https://open.mimiro.io/specifications/uda/latest.html). This data layer can be used in conjunction with the MIMIRO data hub (https://github.com/mimiro-io/datahub) to create a modern data fabric. The MySQL data layer can be configured to expose tables and views from a MySQL database as a stream of changes or a current snapshot. Rows in a table are represented in JSON according the Entity Graph Data model that is described in the UDA specification. This data layer can be run as a standalone binary or as a docker container.

Releases of this data layer are published to docker hub in the repository: `mimiro/mysql-datalayer`

## Testing

You can run
```bash
make testlocal
```
to run the unit tests locally.

## Run

Either do:
```bash
make run
```
or
```bash
make build && bin/server
```

Ensure a config file exists in the location configured in the CONFIG_LOCATION
variable

With Docker

```bash
make docker
docker run -d -p 4343:4343 -v $(pwd)/local.config.json:/root/config.json -e PROFILE=dev -e CONFIG_LOCATION=file://config.json mysql-datalayer
```

## Env

Server will by default use the .env file, AND an extra file per environment,
for example .env-prod if PROFILE is set to "prod". This allows for pr environment
configuration of the environment in addition to the standard ones. All variables
declared in the .env file (but left empty) are available for reading from the ENV
in Docker.

The server will start with a bad or missing configuration file, it has an empty
default file under resources/ that it will load instead, and in general a call
to a misconfigured server should just return empty results or 404's.

Every 60s (or otherwise configured) the server will look for updated config's, and
load these if it detect changes. It should also then "fix" it's connection if changed.

It supports configuration locations that either start with "file://" or "http(s)://".

```bash
# the default server port, this will be overridden to 8080 in AWS
SERVER_PORT=4343

# how verbose the logger should be
LOG_LEVEL=INFO

# setting up token integration with Auth0
TOKEN_WELL_KNOWN=https://auth.yoursite.io/jwks/.well-known/jwks.json
TOKEN_AUDIENCE=https://api.yoursite.io
TOKEN_ISSUER=https://yoursite.eu.auth0.com/

# statsd agent location, if left empty, statsd collection is turned off
DD_AGENT_HOST=

# if config is read from the file system, refer to the file here, for example "file://.config.json"
CONFIG_LOCATION=

# how often should the system look for changes in the configuration. This uses the cron system to
# schedule jobs at the given interval. If ommitted, the default is every 60s.
CONFIG_REFRESH_INTERVAL=@every 60s

#optional mysql db user and password. These should be provided from secrets injection, but they need
# to be here to be able to be picked up with viper.
MYSQL_DB_USER=
MYSQL_DB_PASSWORD=

```
By default the PROFILE is set to local, to easier be able to run on local machines. This also disables
security features, and must NOT be set to local in . It should be PROFILE=dev or PROFILE=prod.

This also changes the loggers.

## Configuration

The service is configured with either a local json file or a remote variant of the same.
It is strongly recommended leaving the Password and User fields empty.

A complete example can be found under "resources/test/test-config.json"

```json
{
  "DatabaseServer" : "[DB SERVER]",
  "Database" : "[DBNAME]",
  "Password" : "[ADD PASSWORD HERE]",
  "User" : "[USERNAME]",

  "BaseUri" : "http://data.test.io/yourtestnamespace/",
  "Port" : "1433",
  "Schema" : "SalesLT",

  "TableMappings" : [
    {
      "TableName" : "Address",
      "EntityIdConstructor" : "addresses/%s",
      "Types" : [ "http://data.test.io/yourtestnamespace/Customer" ],
      "ColumnMappings" : {
        "AddressId" : {
          "IsIdColumn" : true
        }
      }
    },
    {
      "TableName" : "Product",
      "EntityIdConstructor" : "products/%s",
      "Types" : [ "http://data.test.io/yourtestnamespace/Product" ],
      "ColumnMappings" : {
        "ProductId" : {
          "IsIdColumn" : true
        },
        "ProductCategoryID" : {
          "IsReference" : true,
          "ReferenceTemplate" : "http://data.test.io/yourtestnamespace/categories/%s"
        }
      }
    },
    {
      "TableName" : "Customer",
      "EntityIdConstructor" : "customers/%s",
      "Types" : [ "http://data.test.io/yourtestnamespace/Customer" ],
      "ColumnMappings" : {
        "CustomerId" : {
          "IsIdColumn" : true
        },
        "PasswordHash" : {
          "IgnoreColumn" : true
        },
        "PasswordSalt" : {
          "IgnoreColumn" : true
        },
        "SalesPerson" : {
          "PropertyName" : "SalesPersonName"
        }
      }
    }
  ],
    "postMappings": [
        {
            "datasetName": "datahub.Testdata",
            "tableName": "Testdata",
            "query": "INSERT INTO testdata (id, foo, bar) VALUES (?, ?, ?);",
            "config": {
                "databaseServer": "[DB SERVER]",
                "database": "[DBNAME]",
                "port": "1234",
                "schema": "SalesLT",

                "user": {
                    "type": "direct",
                    "key": "[USERNAME]"
                },
                "password": {
                    "type": "direct",
                    "key": "[PASSWORD]"
                }
            },
            "fieldMappings": [
                {
                    "fieldName": "foo",
                    "order": 1
                },
                {
                    "fieldName": "bar",
                    "order": 2
                }
            ]
        }
    ]
}
```

Support for writing in mysql has been implemented.

Configuration for this is added to the above mentioned config.json file in the section postMappings.

The config section is optional, and is available to allows the layer to read and write to different databases.
The user and password can be retrieved from the environment or set directly in the config.
The latter is achieved by setting the type to direct, the value is then retrieved from the key.
When the config section in postMappings is empty or omitted, the top level database configuration will be used.

The order parameter in fieldMappings is used to retain the order of the fields, in regard to the query.
The id in the query is obtained from the entities' id, with the namespace stripped.
