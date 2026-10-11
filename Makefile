.PHONY: fmt lint test build check release licenses clean

VERSION ?= v0.1.0
OUTPUT_DIR ?= dist

fmt:
	go fmt ./...

lint:
	go vet ./...

test:
	go test ./...

build:
	go build -o bin/komposyn-lg ./plugins/cli/token-logger

check: fmt lint test build

release:
	bash scripts/build-release.sh $(VERSION) $(OUTPUT_DIR)

licenses:
	bash scripts/generate-third-party-licenses.sh $(OUTPUT_DIR)

clean:
	rm -rf bin
