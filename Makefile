.PHONY: build test lint check

build:
	go build ./...

test:
	go test ./...

lint:
	@files=$(gofmt -l .) || exit 1; if [ -n "$files" ]; then printf 'Unformatted Go files:\n%s\n' "$files"; exit 1; fi
	go vet ./...

check: lint test build
