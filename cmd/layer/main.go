package main

import (
	common "github.com/mimiro-io/common-datalayer"
	mysql "github.com/mimiro-io/mysql-datalayer/internal/layer"

	"os"
)

func main() {
	configFolderLocation := ""
	args := os.Args[1:]
	if len(args) >= 1 {
		configFolderLocation = args[0]
	}

	common.NewServiceRunner(mysql.NewMysqlDataLayer).WithConfigLocation(configFolderLocation).WithEnrichConfig(mysql.EnrichConfig).StartAndWait()
}
