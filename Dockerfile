FROM golang:1.24-alpine3.21 AS build

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source from the current directory to the Working Directory inside the container
COPY cmd ./cmd
COPY internal ./internal
COPY resources ./resources
COPY *.go ./

# Build the Go app
RUN go build -a -o server cmd/mysql/main.go
RUN go build -a -o mysql-layer cmd/layer/main.go

# tests
RUN go vet ./...

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /app/server /
COPY --from=build /app/mysql-layer /

ADD .env /
ADD resources/default-config.json /resources/

# Expose port 8080 to the outside world
EXPOSE 8080

CMD ["/server"]
