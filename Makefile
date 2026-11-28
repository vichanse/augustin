.PHONY: build test lint check eval eval-real

build:
	go build ./...

test:
	go test ./...

lint:
	@files=$$(gofmt -l .) || exit 1; if [ -n "$$files" ]; then printf 'Unformatted Go files:\n%s\n' "$$files"; exit 1; fi
	go vet ./...

check: lint test build

# Deterministic evaluation: scripted model, no network, no API key.
eval:
	go run ./cmd/augustin-eval --mode deterministic

# Real-model evaluation: explicit opt-in, needs the provider configuration and
# AUGUSTIN_ALLOW_REMOTE_DATA=true. Never part of check. Example:
#   make eval-real EVAL_ARGS="--case discount --repeat 3 --json eval.json"
eval-real:
	go run ./cmd/augustin-eval --mode real $(or $(EVAL_ARGS),--all)
