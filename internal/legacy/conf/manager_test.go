package conf

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mimiro-io/mysql-datalayer/internal/legacy/security"
	"go.uber.org/zap"
)

func TestLoadUrl(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	cmgr := ConfigurationManager{
		logger:         zap.NewNop().Sugar(),
		TokenProviders: security.NoOpTokenProviders(),
	}

	_, err := cmgr.loadUrl(fmt.Sprintf("%s/test/config.json", srv.URL))
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
}

func TestParse(t *testing.T) {
	cmgr := ConfigurationManager{
		logger: zap.NewNop().Sugar(),
	}

	res, err := cmgr.loadFile("../../../resources/test/test-config.json")
	if err != nil {
		t.FailNow()
	}

	config, err := cmgr.parse(res)
	if err != nil {
		t.FailNow()
	}
	if config.Database != "testdb" {
		t.Errorf("%s != testdb", config.Database)
	}
}

func serverMock() *httptest.Server {
	handler := http.NewServeMux()
	handler.HandleFunc("/test/config.json", configMock)
	srv := httptest.NewServer(handler)
	return srv
}

func configMock(w http.ResponseWriter, r *http.Request) {
	cmgr := ConfigurationManager{
		logger: zap.NewNop().Sugar(),
	}
	res, _ := cmgr.loadFile("../../../resources/test/test-config.json")
	_, _ = w.Write(res)
}
