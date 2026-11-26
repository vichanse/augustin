.PHONY: build test lint check

build:
	go build ./...

test:
	go test ./...

lint:
	@test -z "$$(gofmt -l .)"
	go vet ./...

check: lint test build
