.PHONY: fmt verify-modules lint test check

fmt:
	go fmt ./...

verify-modules:
	go mod verify

lint:
	go vet ./...

test:
	go test ./...

check: fmt verify-modules lint test
