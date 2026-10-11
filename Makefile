.PHONY: fmt lint test build check clean

fmt:
	go fmt ./...

lint:
	go vet ./...

test:
	go test ./...

build:
	go build -o bin/komposyn-lg ./plugins/cli/token-logger

check: fmt lint test build

clean:
	rm -rf bin
