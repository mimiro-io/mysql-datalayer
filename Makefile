build:
	go build -o bin/server cmd/mysql/main.go

run:
	go run cmd/mysql/main.go

docker:
	docker build . -t mysql-datalayer

test:
	go vet ./...
	go test ./... -v

testlocal:
	go vet ./...
	go test ./... -v

integration:
	go test ./... -v -tags=integration
