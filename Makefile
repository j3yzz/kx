.PHONY: build test lint

build:
	go build -ldflags "-X github.com/j3yzz/kx/cmd.version=$$(git describe --tags --always --dirty)" -o bin/kx .

test:
	go test ./...

lint:
	go vet ./...
