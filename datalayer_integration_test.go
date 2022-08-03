//go:build integration

package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/docker/go-connections/nat"
	"github.com/franela/goblin"
	"github.com/go-sql-driver/mysql"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/fx"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// we need to create the database manually
func createAndOpen(connectString, dbname string) *sql.DB {
	db, err := sql.Open("mysql", connectString)
	if err != nil {
		panic(err)
	}

	_, err = db.Exec("CREATE DATABASE IF NOT EXISTS " + dbname)
	if err != nil {
		panic(err)
	}

	err = db.Close()
	if err != nil {
		panic(err)
	}

	db, err = sql.Open("mysql", connectString+dbname)
	if err != nil {
		panic(err)
	}
	return db
}

type noopLogger struct{}

func (n noopLogger) Print(v ...interface{}) {
	//nothing
}

func TestIntegration(t *testing.T) {
	g := goblin.Goblin(t)

	var app *fx.App
	var conn *sql.Conn

	layerUrl := "http://localhost:17777/datasets/users"
	g.Describe("The dataset endpoint", func() {
		var mysqlC testcontainers.Container
		g.Before(func() {
			ctx := context.Background()
			req := testcontainers.ContainerRequest{
				Image:        "mysql:latest",
				ExposedPorts: []string{"3306/tcp"},
				Env:          map[string]string{"MYSQL_ROOT_PASSWORD": "password"},
				WaitingFor: wait.ForSQL("3306", "mysql", func(port nat.Port) string {
					return "root:password@(localhost:" + port.Port() + ")/"
				}),
			}
			var err error
			mysql.SetLogger(noopLogger{})
			mysqlC, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
				ContainerRequest: req,
				Started:          true,
			})
			if err != nil {
				t.Error(err)
			}
			actualPort, _ := mysqlC.MappedPort(ctx, nat.Port("3306/tcp"))
			host, _ := mysqlC.Host(ctx)
			port := actualPort.Port()
			testConf := replaceTestConf("./resources/test/mysql-local.json", host, port, t)
			defer os.Remove(testConf.Name())
			os.Setenv("SERVER_PORT", "17777")
			os.Setenv("AUTHORIZATION_MIDDLEWARE", "noop")
			os.Setenv("CONFIG_LOCATION", "file://"+testConf.Name())
			os.Setenv("MYSQL_DB_USER", "root")
			os.Setenv("MYSQL_DB_PASSWORD", "password")
			connString := "root:password@tcp(" + host + ":" + port + ")/"
			createAndOpen(connString, "test")

			fmt.Println(connString)

			db, err := sqlx.Connect("mysql", connString+"test"+"?parseTime=true")
			if err != nil {
				t.Error(err)
			}

			conn, err := db.Conn(context.Background())
			if err != nil {
				t.Error(err)
			}
			_, err = conn.ExecContext(context.Background(), "CREATE TABLE IF NOT EXISTS users "+
				"(id INT, firstname VARCHAR(15), surname VARCHAR(15), timestamp TIMESTAMP, PRIMARY KEY (id));")
			if err != nil {
				t.Log(err)
			}
			oldOut := os.Stdout
			oldErr := os.Stderr
			devNull, _ := os.Open("/dev/null")
			os.Stdout = devNull
			os.Stderr = devNull

			app, _ = Start(context.Background())

			os.Stdout = oldOut
			os.Stderr = oldErr
			os.Unsetenv("SERVER_PORT")
		})
		g.After(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			if conn != nil {
				conn.Close()
			}
			err := app.Stop(ctx)
			cancel()
			g.Assert(err).IsNil()
			//mysqlC.Terminate(ctx)
		})
		g.It("Should write and read", func() {
			g.Timeout(1 * time.Hour)
			fileBytes, err := ioutil.ReadFile("./resources/test/testdata_1.json")
			g.Assert(err).IsNil()
			payload := strings.NewReader(string(fileBytes))
			res, err := http.Post(layerUrl+"/entities", "application/json", payload)
			g.Assert(err).IsNil()
			g.Assert(res).IsNotZero()
			g.Assert(res.Status).Eql("200 OK")

			resp, err := http.Get(layerUrl + "/entities")
			g.Assert(err).IsNil()
			g.Assert(resp).IsNotZero()
			g.Assert(resp.Status).Eql("200 OK")

			body, err := ioutil.ReadAll(resp.Body)
			bodyString := string(body)
			//fmt.Println(bodyString)
			g.Assert(bodyString).IsNotNil()
			g.Assert(strings.Contains(bodyString, "{\"ns0:firstname\":\"Hank\",\"ns0:id\":1,\"ns0:surname\":\"The Tank\",\"ns0:timestamp\":\"2007-02-16T00:00:00Z\"}")).IsTrue()
			g.Assert(strings.Contains(bodyString, "{\"ns0:firstname\":\"Frank\",\"ns0:id\":2,\"ns0:surname\":\"The Tank\",\"ns0:timestamp\":\"2007-02-17T00:00:00Z\"}")).IsTrue()
		})
		g.It("Should delete entity when deleted flag is true", func() {
			g.Timeout(1 * time.Hour)
			fileBytes, err := ioutil.ReadFile("./resources/test/testdata_2.json")
			g.Assert(err).IsNil()
			payload := strings.NewReader(string(fileBytes))

			res, err := http.Post(layerUrl+"/entities", "application/json", payload)
			g.Assert(err).IsNil()
			g.Assert(res).IsNotZero()
			g.Assert(res.Status).Eql("200 OK")

			resp, err := http.Get(layerUrl + "/entities")
			g.Assert(err).IsNil()
			g.Assert(resp).IsNotZero()
			g.Assert(resp.Status).Eql("200 OK")

			body, err := ioutil.ReadAll(resp.Body)
			bodyString := string(body)
			//fmt.Println(bodyString)
			g.Assert(bodyString).IsNotNil()
			g.Assert(strings.Contains(bodyString, "{\"ns0:firstname\":\"Hank\",\"ns0:id\":1,\"ns0:surname\":\"The Tank\",\"ns0:timestamp\":\"2007-02-16T00:00:00Z\"}")).IsFalse()
			g.Assert(strings.Contains(bodyString, "{\"ns0:firstname\":\"Frank\",\"ns0:id\":2,\"ns0:surname\":\"The Hank\",\"ns0:timestamp\":\"2007-02-17T00:00:00Z\"}")).IsTrue()
		})
		g.It("Should write and read entities from the database in the correct order", func() {
			g.Timeout(1 * time.Hour)
			fileBytes, err := ioutil.ReadFile("./resources/test/testdata_1.json")
			g.Assert(err).IsNil()
			payload := strings.NewReader(string(fileBytes))
			res, err := http.Post(layerUrl+"/entities", "application/json", payload)
			g.Assert(err).IsNil()
			g.Assert(res).IsNotZero()
			g.Assert(res.Status).Eql("200 OK")

			resp, err := http.Get(layerUrl + "/entities")
			g.Assert(err).IsNil()
			g.Assert(resp).IsNotZero()
			g.Assert(resp.Status).Eql("200 OK")

			body, err := ioutil.ReadAll(resp.Body)
			bodyString := string(body)
			//fmt.Println(bodyString)
			g.Assert(bodyString).IsNotNil()
			g.Assert(strings.Contains(bodyString, "{\"id\":\"@context\",\"namespaces\":{\"ns0\":\"http://data.test.io/user/user/\",\"rdf\":\"http://www.w3.org/1999/02/22-rdf-syntax-ns#\"}}\n,"+
				"{\"id\":\"http://data.test.io/user/users/1\",\"deleted\":false,\"refs\":{\"rdf:type\":\"http://data.test.io/users\"},\"props\":{\"ns0:firstname\":\"Hank\",\"ns0:id\":1,\"ns0:surname\":\"The Tank\",\"ns0:timestamp\":\"2007-02-16T00:00:00Z\"}}\n,"+
				"{\"id\":\"http://data.test.io/user/users/2\",\"deleted\":false,\"refs\":{\"rdf:type\":\"http://data.test.io/users\"},\"props\":{\"ns0:firstname\":\"Frank\",\"ns0:id\":2,\"ns0:surname\":\"The Tank\",\"ns0:timestamp\":\"2007-02-17T00:00:00Z\"}}")).IsTrue()

			fileBytes, err = ioutil.ReadFile("./resources/test/testdata_3.json")
			g.Assert(err).IsNil()
			payload = strings.NewReader(string(fileBytes))
			res, err = http.Post(layerUrl+"/entities", "application/json", payload)
			g.Assert(err).IsNil()
			g.Assert(res).IsNotZero()
			g.Assert(res.Status).Eql("200 OK")

			resp, err = http.Get(layerUrl + "/entities")
			g.Assert(err).IsNil()
			g.Assert(resp).IsNotZero()
			g.Assert(resp.Status).Eql("200 OK")

			body, err = ioutil.ReadAll(resp.Body)
			bodyString = string(body)
			//fmt.Println(bodyString)
			g.Assert(bodyString).IsNotNil()
			g.Assert(strings.Contains(bodyString, "{\"id\":\"@context\",\"namespaces\":{\"ns0\":\"http://data.test.io/user/user/\",\"rdf\":\"http://www.w3.org/1999/02/22-rdf-syntax-ns#\"}}\n,"+
				"{\"id\":\"http://data.test.io/user/users/1\",\"deleted\":false,\"refs\":{\"rdf:type\":\"http://data.test.io/users\"},\"props\":{\"ns0:firstname\":\"Hank\",\"ns0:id\":1,\"ns0:surname\":\"The Tank\",\"ns0:timestamp\":\"2007-02-16T00:00:00Z\"}}\n,"+
				"{\"id\":\"http://data.test.io/user/users/2\",\"deleted\":false,\"refs\":{\"rdf:type\":\"http://data.test.io/users\"},\"props\":{\"ns0:firstname\":\"Frank\",\"ns0:id\":2,\"ns0:surname\":\"The Tank\",\"ns0:timestamp\":\"2007-02-17T00:00:00Z\"}}\n,"+
				"{\"id\":\"http://data.test.io/user/users/3\",\"deleted\":false,\"refs\":{\"rdf:type\":\"http://data.test.io/users\"},\"props\":{\"ns0:firstname\":\"Frank\",\"ns0:id\":3,\"ns0:surname\":\"and Hank\",\"ns0:timestamp\":\"2007-02-16T00:00:00Z\"}}\n,"+
				"{\"id\":\"http://data.test.io/user/users/4\",\"deleted\":false,\"refs\":{\"rdf:type\":\"http://data.test.io/users\"},\"props\":{\"ns0:firstname\":\"Hank\",\"ns0:id\":4,\"ns0:surname\":\"The Frank\",\"ns0:timestamp\":\"2007-02-17T00:00:00Z\"}}")).IsTrue()
		})

	})
}

func replaceTestConf(fileTemplate string, host string, port string, t *testing.T) *os.File {
	bts, err := ioutil.ReadFile(fileTemplate)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.ReplaceAll(string(bts), "localhost", host)
	content = strings.ReplaceAll(content, "3306", port)
	tmpfile, err := ioutil.TempFile(".", "integration-test.json")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}
	return tmpfile
}
