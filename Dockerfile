FROM golang:1.22.2 as builder

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download


FROM builder as build

# Copy the source from the current directory to the Working Directory inside the container
COPY cmd ./cmd
COPY internal ./internal
COPY resources ./resources
COPY *.go ./

# Build the Go app
RUN CGO_ENABLED=0 GOOS=linux go build -a -o server cmd/mysql/main.go

RUN go vet ./...
RUN go test ./... -v


FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=build /app/server .
ADD .env .
ADD resources/default-config.json resources/

# Expose port 8080 to the outside world
EXPOSE 8080

CMD ["./server"]
