//go:build integration

package layer

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	common "github.com/mimiro-io/common-datalayer"
	egdm "github.com/mimiro-io/entity-graph-data-model"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	testMySQLImage    = "mysql:8.0"
	testMySQLUser     = "root"
	testMySQLPassword = "password"
	testMySQLDatabase = "myapp"
)

func setupMySQLContainer(t *testing.T) *sql.DB {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("skipping integration test because docker is unavailable: %v", err)
	}

	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        testMySQLImage,
		ExposedPorts: []string{"3306/tcp"},
		Env: map[string]string{
			"MYSQL_ROOT_PASSWORD": testMySQLPassword,
			"MYSQL_DATABASE":      testMySQLDatabase,
		},
		WaitingFor: wait.ForLog("port: 3306  MySQL Community Server - GPL").WithStartupTimeout(2 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("skipping integration test because MySQL container could not be started: %v", err)
	}

	t.Cleanup(func() {
		terminateCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := container.Terminate(terminateCtx); err != nil {
			t.Logf("failed to terminate MySQL container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to resolve MySQL container host: %v", err)
	}

	port, err := container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("failed to resolve MySQL container port: %v", err)
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&multiStatements=true",
		testMySQLUser,
		testMySQLPassword,
		host,
		port.Port(),
		testMySQLDatabase,
	)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("failed to open database connection: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Logf("failed to close database connection: %v", err)
		}
	})

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if err := db.PingContext(waitCtx); err == nil {
			break
		} else if waitCtx.Err() != nil {
			t.Fatalf("mysql did not become ready in time: %v", waitCtx.Err())
		}
		<-ticker.C
	}

	schema := `CREATE TABLE IF NOT EXISTS product (
        id VARCHAR(50) PRIMARY KEY,
        product_id INT,
        productprice INT,
        date DATETIME,
        reporter VARCHAR(50),
        timestamp DATETIME(6),
        version INT,
        date_test DATE,
        datetime_test DATETIME
    );`

	if _, err := db.ExecContext(ctx, schema); err != nil {
		t.Fatalf("failed to create product table: %v", err)
	}

	if _, err := db.ExecContext(ctx, "TRUNCATE TABLE product"); err != nil {
		t.Fatalf("failed to truncate product table: %v", err)
	}

	return db
}

func buildTestDataset(db *sql.DB) *Dataset {
	incoming := &common.IncomingMappingConfig{
		BaseURI: "http://data.test.io/newtestnamespace/product/",
		PropertyMappings: []*common.EntityToItemPropertyMapping{
			{Property: "id", IsIdentity: true, StripReferencePrefix: true},
			{Property: "product_id", EntityProperty: "Product_Id"},
			{Property: "productprice", EntityProperty: "ProductPrice"},
			{Property: "date", EntityProperty: "Date", Datatype: "datetime"},
			{Property: "reporter", EntityProperty: "Reporter"},
			{Property: "version", EntityProperty: "Version"},
			{Property: "date_test", EntityProperty: "DateTest", DefaultValue: "2008-11-30"},
			{Property: "datetime_test", EntityProperty: "DateTimeTest", Datatype: "datetime"},
		},
	}

	outgoing := &common.OutgoingMappingConfig{
		BaseURI: "http://data.sample.org/",
		PropertyMappings: []*common.ItemToEntityPropertyMapping{
			{Property: "id", IsIdentity: true, URIValuePattern: "http://data.sample.org/things/{value}"},
			{Property: "product_id", EntityProperty: "product_id"},
			{Property: "productprice", EntityProperty: "productprice"},
			{Property: "reporter", EntityProperty: "reporter"},
			{Property: "date_test", EntityProperty: "date_test"},
			{Property: "datetime_test", EntityProperty: "datetime_test", Datatype: "datetime"},
		},
	}

	definition := &common.DatasetDefinition{
		DatasetName: "products",
		SourceConfig: map[string]any{
			TableName:      "product",
			SinceColumn:    "timestamp",
			SinceTable:     "product",
			SincePrecision: "6",
			FlushThreshold: float64(1000),
		},
		IncomingMappingConfig: incoming,
		OutgoingMappingConfig: outgoing,
	}

	logger := common.NewLogger("mysql-test", "json", "error")
	return &Dataset{
		logger:            logger,
		db:                &MysqlDB{db: db},
		datasetDefinition: definition,
	}
}

func makeProductEntity(id string, productID, price int, recorded uint64, when time.Time) *egdm.Entity {
	base := "http://data.test.io/newtestnamespace/product/"
	entity := egdm.NewEntity()
	entity.ID = base + id
	entity.Recorded = recorded
	entity.Properties[base+"Product_Id"] = productID
	entity.Properties[base+"ProductPrice"] = price
	entity.Properties[base+"Date"] = when.UTC().Format(time.RFC3339)
	entity.Properties[base+"Reporter"] = fmt.Sprintf("reporter-%s", id)
	entity.Properties[base+"Version"] = 1
	entity.Properties[base+"DateTest"] = "2008-11-30"
	entity.Properties[base+"DateTimeTest"] = when.UTC().Format(time.RFC3339)
	return entity
}

func TestDatasetWriteAndReadWithMySQL(t *testing.T) {
	db := setupMySQLContainer(t)
	if db == nil {
		return
	}

	dataset := buildTestDataset(db)
	ctx := context.Background()

	writer, err := dataset.Incremental(ctx)
	if err != nil {
		t.Fatalf("failed to acquire dataset writer: %v", err)
	}

	now := time.Now()
	entities := []*egdm.Entity{
		makeProductEntity("1", 3101, 9900, uint64(now.UnixNano()), now),
		makeProductEntity("2", 3102, 14900, uint64(now.Add(time.Second).UnixNano()), now.Add(time.Second)),
	}

	for _, entity := range entities {
		if err := writer.Write(entity); err != nil {
			t.Fatalf("failed to write entity %s: %v", entity.ID, err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM product").Scan(&count); err != nil {
		t.Fatalf("failed to count rows: %v", err)
	}

	if count != len(entities) {
		t.Fatalf("expected %d rows in product table, got %d", len(entities), count)
	}

	iterator, err := dataset.Changes("", 10, false)
	if err != nil {
		t.Fatalf("failed to obtain dataset iterator: %v", err)
	}

	seen := map[string]*egdm.Entity{}
	for {
		entity, err := iterator.Next()
		if err != nil {
			t.Fatalf("iterator failed: %v", err)
		}
		if entity == nil {
			break
		}
		seen[entity.ID] = entity
	}

	if err := iterator.Close(); err != nil {
		t.Fatalf("failed to close iterator: %v", err)
	}

	if len(seen) != len(entities) {
		t.Fatalf("expected %d entities returned from dataset, got %d", len(entities), len(seen))
	}

	for idx := range entities {
		expectedID := fmt.Sprintf("http://data.sample.org/things/%d", idx+1)
		entity, ok := seen[expectedID]
		if !ok {
			t.Fatalf("expected entity with id %s to be returned", expectedID)
		}

		productIDKey := "http://data.sample.org/product_id"
		if fmt.Sprint(entity.Properties[productIDKey]) != fmt.Sprint(3100+idx+1) {
			t.Fatalf("unexpected product_id for %s: %v", expectedID, entity.Properties[productIDKey])
		}

		priceKey := "http://data.sample.org/productprice"
		if fmt.Sprint(entity.Properties[priceKey]) != fmt.Sprint([]int{9900, 14900}[idx]) {
			t.Fatalf("unexpected productprice for %s: %v", expectedID, entity.Properties[priceKey])
		}

		reporterKey := "http://data.sample.org/reporter"
		expectedReporter := fmt.Sprintf("reporter-%d", idx+1)
		if entity.Properties[reporterKey] != expectedReporter {
			t.Fatalf("unexpected reporter for %s: %v", expectedID, entity.Properties[reporterKey])
		}
	}
}
