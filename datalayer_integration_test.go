//go:build integration
// +build integration

package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/franela/goblin"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/fx"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIntegration(t *testing.T) {
	g := goblin.Goblin(t)
	var app *fx.App
	var conn *sql.Conn
	layerUrl := "http://localhost:17777/datasets/Product"
	g.Describe("The dataset endpoint", func() {
		var mysqlC testcontainers.Container
		g.Before(func() {
			ctx := context.Background()
			req := testcontainers.ContainerRequest{
				Image:        "mysql:latest",
				ExposedPorts: []string{"3306/tcp"},
				Env: map[string]string{
					"MYSQL_ROOT_PASSWORD": "password",
					"MYSQL_DATABASE":      "myapp",
				},
				WaitingFor: wait.ForLog("port: 3306  MySQL Community Server - GPL"),
			}
			var err error
			mysqlC, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
				ContainerRequest: req,
				Started:          true,
			})
			if err != nil {
				t.Fatal(err)
			}
			actualPort, _ := mysqlC.MappedPort(ctx, "3306")
			ip, _ := mysqlC.Host(ctx)
			port := actualPort.Port()
			testConf := replaceTestConf("./resources/test/test-config.json", ip, port, t)
			defer os.Remove(testConf.Name())
			os.Setenv("SERVER_PORT", "17777")
			os.Setenv("AUTHORIZATION_MIDDLEWARE", "noop")
			os.Setenv("CONFIG_LOCATION", "file://"+testConf.Name())
			os.Setenv("MYSQL_DB_USER", "root")
			os.Setenv("MYSQL_DB_PASSWORD", "password")
			dsn := fmt.Sprintf("root:password@tcp(%s:%s)/myapp?parseTime=true&multiStatements=true", ip, port)
			db, err := sql.Open("mysql", dsn)
			defer db.Close()
			conn, err = db.Conn(ctx)
			if err != nil {
				t.Error(err)
			}
			_, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS product "+
				"(id INT PRIMARY KEY, product_id INT, productprice INT, date DATETIME, "+
				"reporter VARCHAR(15), timestamp DATETIME, version INT);")
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
			//os.Unsetenv("SERVER_PORT")
		})
		g.After(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			err := app.Stop(ctx)
			g.Assert(err).IsNil()
			mysqlC.Terminate(ctx)
		})

		g.It("Should accept a payload without error", func() {
			fileBytes, err := ioutil.ReadFile("./resources/test/testdata_1.json")
			g.Assert(err).IsNil()
			payload := strings.NewReader(string(fileBytes))
			res, err := http.Post(layerUrl+"/entities", "application/json", payload)
			g.Assert(err).IsNil()
			g.Assert(res).IsNotZero()
			g.Assert(res.Status).Eql("200 OK")
		})
		g.It("should return number of rows in table product", func() {
			var count int
			err := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM product").Scan(&count)
			g.Assert(err).IsNil()
			g.Assert(count).Equal(10)
		})
		g.It("should read changes back from table", func() {
			fileBytes, _ := ioutil.ReadFile("./resources/test/testdata_2.json")
			payload := strings.NewReader(string(fileBytes))
			http.Post(layerUrl+"/entities", "application/json", payload)

			res, err := http.Get(layerUrl + "/changes")
			g.Assert(err).IsNil()
			b, _ := io.ReadAll(res.Body)
			//t.Log(string(b))
			g.Assert(len(b)).Equal(1760)
		})
		g.It("should read entities back from table", func() {
			fileBytes, _ := ioutil.ReadFile("./resources/test/testdata_2.json")
			payload := strings.NewReader(string(fileBytes))
			http.Post(layerUrl+"/entities", "application/json", payload)

			res, err := http.Get(layerUrl + "/entities")
			g.Assert(err).IsNil()
			b, _ := io.ReadAll(res.Body)
			//t.Log(string(b))
			g.Assert(len(b)).Equal(1760)
		})
		// Not crucial to test now, commenting for the future
		/*g.It("should delete entities where deleted flag set to true when entity already exist in table", func() {
			//Assert that the one we want to delete is there
			var count int
			err := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM product WHERE id = 1").Scan(&count)
			g.Assert(err).IsNil()
			g.Assert(count).Equal(1)

			//Assert that there are 10 entities in the table already
			err = conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM product").Scan(&count)
			g.Assert(err).IsNil()
			g.Assert(count).Equal(10)

			//Modify table entries
			fileBytes, err := ioutil.ReadFile("./resources/test/testdata_2.json")
			g.Assert(err).IsNil()
			payload := strings.NewReader(string(fileBytes))
			res, err := http.Post(layerUrl+"/entities", "application/json", payload)
			g.Assert(err).IsNil()
			g.Assert(res).IsNotZero()

			//Assert that one is deleted
			err = conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM product").Scan(&count)
			g.Assert(err).IsNil()
			g.Assert(count).Equal(9)

			//Assert that the first entity with id=1 is the one that has been deleted
			err = conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM product WHERE id = 1").Scan(&count)
			g.Assert(err).IsNil()
			g.Assert(count).Equal(0)
		})

		g.It("should not store entity if flag is set to deleted = true", func() {
			res, err := conn.ExecContext(context.Background(), "TRUNCATE TABLE product")
			g.Assert(err).IsNil()
			g.Assert(res).IsNotZero()

			fileBytes, err := ioutil.ReadFile("./resources/test/testdata_2.json")
			g.Assert(err).IsNil()
			payload := strings.NewReader(string(fileBytes))
			result, err := http.Post(layerUrl+"/entities", "application/json", payload)
			g.Assert(err).IsNil()
			g.Assert(result).IsNotZero()

			//Assert that one of entity is not stored
			var count int
			err = conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM product").Scan(&count)
			g.Assert(err).IsNil()
			g.Assert(count).Equal(9)

			//Assert that the first entity with id=1 is the one that is not stored
			err = conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM product WHERE id = 1").Scan(&count)
			g.Assert(err).IsNil()
			g.Assert(count).Equal(0)

		})*/
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
