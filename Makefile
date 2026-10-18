.PHONY: build test lint

build:
	go build -o bin/kx .

test:
	go test ./...

lint:
	go vet ./...
