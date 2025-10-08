package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	common "github.com/mimiro-io/common-datalayer"
	egdm "github.com/mimiro-io/entity-graph-data-model"
	mysql "github.com/mimiro-io/mysql-datalayer/internal/layer"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	conn     *sql.Conn
	service  *common.ServiceRunner
	layerUrl = "http://localhost:17777/datasets/"
)

func setup(t *testing.T) testcontainers.Container {
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
	MysqlC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	actualPort, _ := MysqlC.MappedPort(ctx, "3306")
	ip, _ := MysqlC.Host(ctx)
	port := actualPort.Port()

	service = common.NewServiceRunner(mysql.NewMysqlDataLayer).WithConfigLocation("./resources/layer")
	service = service.WithEnrichConfig(func(config *common.Config) error {
		config.NativeSystemConfig["host"] = "localhost"
		config.NativeSystemConfig["port"] = port
		return nil
	})
	go service.StartAndWait()

	dsn := fmt.Sprintf("root:password@tcp(%s:%s)/myapp?parseTime=true&multiStatements=true", ip, port)
	db, err := sql.Open("mysql", dsn)
	defer db.Close()
	conn, err = db.Conn(ctx)
	if err != nil {
		t.Error(err)
	}
	_, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS product "+
		"(id VARCHAR(50) PRIMARY KEY, product_id INT, productprice INT, date DATETIME, "+
		"reporter VARCHAR(15), timestamp DATETIME(6), version INT, date_test DATE, datetime_test DATETIME);")
	if err != nil {
		t.Log(err)
	}
	stmt := "CREATE TABLE IF NOT EXISTS customer " +
		"(id VARCHAR(50) PRIMARY KEY, entity JSON);"
	_, err = conn.ExecContext(context.Background(), stmt)
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// add some insert statements to add some entity objects to the customer table
	customerList := []struct {
		ID     string
		Entity string
	}{
		{"http://data.example.io/customers/1", `{"id": "http://data.example.io/customers/1"}`},
		{"http://data.example.io/customers/2", `{"id": "http://data.example.io/customers/2"}`},
		{"http://data.example.io/customers/3", `{"id": "http://data.example.io/customers/3"}`},
		{"http://data.example.io/customers/4", `{"id": "http://data.example.io/customers/4"}`},
	}
	for _, customer := range customerList {
		_, err = conn.ExecContext(context.Background(), "INSERT INTO customer (id, entity) VALUES (?, ?)", customer.ID, customer.Entity)
		if err != nil {
			t.Fatalf("Failed to insert data into customer table: %v", err)
		}
	}

	return MysqlC
}

func teardown(t *testing.T, MysqlC testcontainers.Container) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	conn.Close()
	cancel()
	MysqlC.Terminate(ctx)
	service.Stop()
}

func populateProductsTable(amount int, conn *sql.Conn, t *testing.T) {
	type product struct {
		ID           string
		ProductId    int
		ProductPrice int
		Date         time.Time
		Reporter     string
		Timestamp    time.Time
		Version      int
		DateTest     time.Time
		DatetimeTest time.Time
	}

	var products []product

	for i := 0; i < amount; i++ {
		id := i + 1
		p := product{fmt.Sprint(id), id, id * 100, time.Now(), fmt.Sprintf("reporter%d", id), time.Now(), 1, time.Now(), time.Now()}
		products = append(products, p)
	}

	for _, product := range products {
		_, err := conn.ExecContext(
			context.Background(),
			"INSERT INTO product (id, product_id, productprice, date, reporter, timestamp, version, date_test, datetime_test) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			product.ID, product.ProductId, product.ProductPrice, product.Date, product.Reporter, product.Timestamp, product.Version, product.DateTest, product.DatetimeTest)
		if err != nil {
			t.Fatalf("Failed to insert data into product table: %v", err)
		}
	}
}

func emptyProductsTable(conn *sql.Conn, t *testing.T) {
	_, err := conn.ExecContext(context.Background(), "DELETE FROM product")
	if err != nil {
		t.Fatalf("Failed to empty product table: %v", err)
	}
}

func TestDatasetEndpoint(t *testing.T) {
	MysqlC := setup(t)
	defer teardown(t, MysqlC)

	t.Run("Should accept a payload without error", func(t *testing.T) {
		fileBytes, err := os.ReadFile("./resources/test/testdata_1.json")
		if err != nil {
			t.Fatal(err)
		}
		payload := strings.NewReader(string(fileBytes))
		res, err := http.Post(layerUrl+"products/entities", "application/json", payload)
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("Unexpected response: %v", err)
		}
		emptyProductsTable(conn, t)
	})

	t.Run("Should return number of rows in table product", func(t *testing.T) {
		populateProductsTable(10, conn, t)

		var count int
		if err := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM product").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 10 {
			t.Fatalf("Expected 10 rows, got %d", count)
		}
		emptyProductsTable(conn, t)
	})

	t.Run("Should delete entities where deleted flag is true", func(t *testing.T) {
		populateProductsTable(10, conn, t)

		var count int
		conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM product WHERE id = 1").Scan(&count)
		if count != 1 {
			t.Fatalf("Expected one row with id=1, got %d", count)
		}

		fileBytes, _ := os.ReadFile("./resources/test/testdata_2.json")
		payload := strings.NewReader(string(fileBytes))
		res, err := http.Post(layerUrl+"products/entities", "application/json", payload)
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("Unexpected response: %v", err)
		}

		conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM product").Scan(&count)
		if count != 9 {
			t.Fatalf("Expected 9 rows after deletion, got %d", count)
		}

		emptyProductsTable(conn, t)
	})

	t.Run("Should set a default value if property is missing", func(t *testing.T) {
		fileBytes, err := os.ReadFile("./resources/test/testdata_1.json")
		if err != nil {
			t.Fatal(err)
		}
		payload := strings.NewReader(string(fileBytes))
		http.Post(layerUrl+"products/entities", "application/json", payload)

		res, err := http.Get(layerUrl + "products/entities")

		if err != nil {
			t.Fatal(err)
		}

		// entity graph data model
		entityParser := egdm.NewEntityParser(egdm.NewNamespaceContext()).WithExpandURIs()
		ec, err := entityParser.LoadEntityCollection(res.Body)

		if err != nil {
			t.Fatal(err)
		}

		if len(ec.Entities) != 10 {
			t.Fatalf("Expected 10 entities, got %d", len(ec.Entities))
		}
		if ec.Entities[1].Properties["http://data.sample.org/date_test"] != "2008-11-30T00:00:00Z" {
			t.Fatalf("Expected date_test to be '2008-11-30T00:00:00Z', got '%s'", ec.Entities[1].Properties["date_test"])
		}
		emptyProductsTable(conn, t)
	})

	t.Run("Should not set column if property is missing and no default_value", func(t *testing.T) {
		fileBytes, err := os.ReadFile("./resources/test/testdata_4.json")
		if err != nil {
			t.Fatal(err)
		}
		payload := strings.NewReader(string(fileBytes))
		http.Post(layerUrl+"products3/entities", "application/json", payload)

		res, err := http.Get(layerUrl + "products3/entities")

		if err != nil {
			t.Fatal(err)
		}

		// entity graph data model
		entityParser := egdm.NewEntityParser(egdm.NewNamespaceContext()).WithExpandURIs()
		ec, err := entityParser.LoadEntityCollection(res.Body)

		if err != nil {
			t.Fatal(err)
		}

		if len(ec.Entities) != 10 {
			t.Fatalf("Expected 10 entities, got %d", len(ec.Entities))
		}
		if ec.Entities[7].Properties["http://data.sample.org/version"] != 987654321.0000 {
			t.Fatalf("Expected version to be 987654321, got '%f'", ec.Entities[7].Properties["http://data.sample.org/version"])
		}
		emptyProductsTable(conn, t)
	})
	t.Run("Should read changes back from table", func(t *testing.T) {
		fileBytes, _ := os.ReadFile("./resources/test/testdata_2.json")
		payload := strings.NewReader(string(fileBytes))
		http.Post(layerUrl+"products/entities", "application/json", payload)

		res, err := http.Get(layerUrl + "products/changes")
		if err != nil {
			t.Fatal(err)
		}

		// entity graph data model
		entityParser := egdm.NewEntityParser(egdm.NewNamespaceContext()).WithExpandURIs()
		ec, err := entityParser.LoadEntityCollection(res.Body)

		if err != nil {
			t.Fatal(err)
		}

		if len(ec.Entities) != 9 {
			t.Fatalf("Expected 9 entities, got %d", len(ec.Entities))
		}
		emptyProductsTable(conn, t)
	})

	t.Run("Should read changes based on continuation token", func(t *testing.T) {
		fileBytes, _ := os.ReadFile("./resources/test/testdata_1.json")
		payload := strings.NewReader(string(fileBytes))
		http.Post(layerUrl+"products/entities", "application/json", payload)

		res, err := http.Get(layerUrl + "products/changes")
		if err != nil {
			t.Fatal(err)
		}

		// entity graph data model
		entityParser := egdm.NewEntityParser(egdm.NewNamespaceContext()).WithExpandURIs()
		ec, err := entityParser.LoadEntityCollection(res.Body)

		if err != nil {
			t.Fatal(err)
		}

		if len(ec.Entities) != 10 {
			t.Fatalf("Expected 10 entities, got %d", len(ec.Entities))
		}

		// get the continuation token
		nextToken := ec.Continuation.Token

		// do a get with the continuation token
		res, err = http.Get(layerUrl + "products/changes?since=" + nextToken)

		if err != nil {
			t.Fatal(err)
		}

		// entity graph data model
		entityParser = egdm.NewEntityParser(egdm.NewNamespaceContext()).WithExpandURIs()
		ec, err = entityParser.LoadEntityCollection(res.Body)

		if err != nil {
			t.Fatal(err)
		}

		if len(ec.Entities) != 0 {
			t.Fatalf("Expected 0 entities, got %d", len(ec.Entities))
		}

		// now send some updates in the form a delete
		fileBytes, _ = os.ReadFile("./resources/test/testdata_2.json")
		payload = strings.NewReader(string(fileBytes))
		http.Post(layerUrl+"products/entities", "application/json", payload)

		// then fetch changes again, there should only be one
		res, err = http.Get(layerUrl + "products/changes?since=" + nextToken)
		if err != nil {
			t.Fatal(err)
		}

		// entity graph data model
		entityParser = egdm.NewEntityParser(egdm.NewNamespaceContext()).WithExpandURIs()
		ec, err = entityParser.LoadEntityCollection(res.Body)

		if err != nil {
			t.Fatal(err)
		}

		if len(ec.Entities) != 9 {
			t.Fatalf("Expected 9 entity, got %d", len(ec.Entities))
		}
		emptyProductsTable(conn, t)
	})

	t.Run("Should read changes based on continuation token and query", func(t *testing.T) {
		fileBytes, _ := os.ReadFile("./resources/test/testdata_1.json")
		payload := strings.NewReader(string(fileBytes))

		res, err := http.Post(layerUrl+"products/entities", "application/json", payload)
		if err != nil {
			t.Fatal(err)
		}
		// using products2 config to use query to get changes
		res, err = http.Get(layerUrl + "products2/changes")
		if err != nil {
			t.Fatal(err)
		}

		// entity graph data model
		entityParser := egdm.NewEntityParser(egdm.NewNamespaceContext()).WithExpandURIs()
		ec, err := entityParser.LoadEntityCollection(res.Body)

		if err != nil {
			t.Fatal(err)
		}

		if len(ec.Entities) != 10 {
			t.Fatalf("Expected 10 entities, got %d", len(ec.Entities))
		}

		// get the continuation token
		nextToken := ec.Continuation.Token
		// do a get with the continuation token

		res, err = http.Get(layerUrl + "products/changes?since=" + nextToken)

		if err != nil {
			t.Fatal(err)
		}

		// entity graph data model
		entityParser = egdm.NewEntityParser(egdm.NewNamespaceContext()).WithExpandURIs()
		ec, err = entityParser.LoadEntityCollection(res.Body)

		if err != nil {
			t.Fatal(err)
		}

		if len(ec.Entities) != 0 {
			t.Fatalf("Expected 0 entities, got %d", len(ec.Entities))
		}

		// now send some updates in the form a delete
		fileBytes, _ = os.ReadFile("./resources/test/testdata_2.json")
		payload = strings.NewReader(string(fileBytes))
		http.Post(layerUrl+"products/entities", "application/json", payload)

		// then fetch changes again, there should only be one
		res, err = http.Get(layerUrl + "products/changes?since=" + nextToken)
		if err != nil {
			t.Fatal(err)
		}

		// entity graph data model
		entityParser = egdm.NewEntityParser(egdm.NewNamespaceContext()).WithExpandURIs()
		ec, err = entityParser.LoadEntityCollection(res.Body)
		if err != nil {
			t.Fatal(err)
		}

		if len(ec.Entities) != 9 {
			t.Fatalf("Expected 9 entity, got %d", len(ec.Entities))
		}
		emptyProductsTable(conn, t)
	})

	t.Run("Should read changes from entity column", func(t *testing.T) {

		res, err := http.Get(layerUrl + "customers/changes")
		if err != nil {
			t.Fatal(err)
		}

		// entity graph data model
		entityParser := egdm.NewEntityParser(egdm.NewNamespaceContext()).WithExpandURIs()
		ec, err := entityParser.LoadEntityCollection(res.Body)

		if err != nil {
			t.Fatal(err)
		}

		if len(ec.Entities) != 4 {
			t.Fatalf("Expected 4 entities, got %d", len(ec.Entities))
		}

	})

	t.Run("Should read changes from empty table", func(t *testing.T) {
		// query to delete all rows in product table
		_, err := conn.ExecContext(context.Background(), "DELETE FROM product")
		if err != nil {
			t.Fatal(err)
		}

		res, err := http.Get(layerUrl + "products2/changes")
		if err != nil {
			t.Fatal(err)
		}

		// entity graph data model
		entityParser := egdm.NewEntityParser(egdm.NewNamespaceContext()).WithExpandURIs()
		ec, err := entityParser.LoadEntityCollection(res.Body)

		if err != nil {
			t.Fatal(err)
		}

		if len(ec.Entities) != 0 {
			t.Fatalf("Expected 0 entities, got %d", len(ec.Entities))
		}
	})

	t.Run("Should delete entities when payload contain only deleted entities", func(t *testing.T) {
		populateProductsTable(3, conn, t)

		// Assert that there are 3 entities in the table
		var count int
		err := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM product").Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatalf("Expected 3 rows, got %d", count)
		}

		fileBytes, _ := os.ReadFile("./resources/test/testdata_5.json")
		payload := strings.NewReader(string(fileBytes))
		res, err := http.Post(layerUrl+"products/entities", "application/json", payload)
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("Unexpected response: %v", err)
		}

		conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM product").Scan(&count)
		if count != 0 {
			t.Fatalf("Expected 0 rows after deletion, got %d", count)
		}
	})
}
