.PHONY: run build tidy vet test

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

tidy:
	go mod tidy

vet:
	go vet ./...

test:
	go test ./...
