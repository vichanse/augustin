# Read-only quickstart

Use Go 1.25 and a trusted disposable synthetic workspace. Do not use customer repositories or secrets. The filename policy is not a secret scanner or sandbox.

Set OPENAI_API_KEY securely in the environment, then explicitly select a compatible model and consent to sending prompts and permitted file contents to OpenAI.

```sh
export AUGUSTIN_PROVIDER=openai
export AUGUSTIN_MODEL=gpt-5-mini
export AUGUSTIN_ALLOW_REMOTE_DATA=true
workspace=$(mktemp -d)
cp evals/fixtures/buggy-discount/discount.go.txt "$workspace/discount.go"
go run ./cmd/augustin run --workspace "$workspace" "Read discount.go and explain the bug."
```

API access is billed separately. The model choice is an example, not a default. stdout contains the final answer; stderr contains metadata-only JSONL events and CLI errors. Exit status 0 means the loop completed, not that its diagnosis is correct. No writes or shell tools are available. Unset the consent variable after use. Real-model verification is a separate milestone.
