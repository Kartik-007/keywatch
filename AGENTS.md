# Working on keywatch

Read SPEC.md first. It is the source of truth for scope and UX.

## Layout

- `internal/core`: shared types (`FoundSecret`, `ProviderKey`, `Key`, `Finding`), the key detector, and `Join`.
- `internal/source/<name>`: one package per platform. Implement `source.Source`, call `source.Register` in `init()`.
- `internal/provider/<name>`: one package per AI provider. Implement `provider.Provider`, call `provider.Register` in `init()`.
- `internal/inventory`: the pipeline every command uses (sources → providers → join → spend).
- `internal/cli`: one file per command. Each file adds its command to `Root` in `init()`. Never edit another command's file.
- `internal/ui`: shared styles, prompts, and the end-of-run receipt.
- `internal/demo`: `--demo` fakes.

## Rules

- **Never print or log a full secret.** Use `core.Redact`. Compare with `core.Fingerprint` or `core.MatchesHint`.
- **Never commit a key-shaped string**, not even a fake one. Build fakes at runtime with `internal/testutil` and use `{{PLACEHOLDERS}}` in testdata.
- **Read-only by default.** Any write (provider, platform, file) shows a plan and calls `ui.Confirm(..., false)`. `--yes` never applies to writes.
- **Every network call is logged when `cli.Verbose` is set:** method + host + path, never headers or bodies.
- **No telemetry, no new network hosts** beyond the provider and platform APIs in SPEC.md.
- **Tests run offline.** Record API responses as JSON in `testdata/<pkg>/` and serve them with `httptest`. Base URLs must be overridable for this.
- Errors from one source or provider never stop the run. Return them; inventory collects them.
- Keep dependencies minimal. Ask before adding one.
- `go build ./... && go vet ./... && go test ./...` must pass before you finish.
