# Repository Guidelines

## Project Structure & Module Organization

`cmd/server` contains the HTTP/WebSocket entry point, configuration, templates, and embedded PWA assets. Backend packages live under `internal/`: handlers, SQLite-backed storage, sessions, telemetry, and version metadata. Browser JavaScript and its tests are in `cmd/server/static`; Go tests sit beside the implementation as `*_test.go`. Operational files are at the root (`Dockerfile`, `compose.yaml`, `Justfile`), documentation is in `README.md` and `docs/`, and helper scripts are in `scripts/`.

## Build, Test, and Development Commands

Use [Just](https://github.com/casey/just) recipes where possible:

- `just run` starts the server with `go run`.
- `just build` creates the `sharetext-server` binary; set `VERSION=v1.2.3` to embed a version.
- `just test` runs all Go tests; `just test-race` adds the race detector.
- `just test-js` runs browser-module tests with Node’s built-in `node:test`; `just test-all` runs both suites.
- `just vet` runs `go vet ./...`; `just fmt` applies `gofmt`.
- `just up`, `just logs`, `just smoke`, and `just down` build, inspect, health-check, and stop the Compose stack.

## Coding Style & Naming Conventions

Run `gofmt` on Go changes and follow idiomatic Go naming: exported identifiers use PascalCase, local identifiers use mixedCase, and errors are handled explicitly. Keep JavaScript modules small and use existing camelCase conventions. Use descriptive filenames and colocate tests with the code they exercise. Do not commit generated binaries, databases, or secrets from `.env`.

## Testing Guidelines

Add Go tests beside production files with names such as `store_test.go`; use table-driven tests for related cases. Add JavaScript tests as `*.test.mjs` beside the module under test. Run `just test-all` before submitting changes, and use `just test-race` when changing concurrency, WebSockets, or shared storage.

## Commit & Pull Request Guidelines

Match the existing concise subject style: begin with an imperative description (for example, `Fix encrypted attachment sync`) and reference an issue when relevant (`Fix #2 ...`). Keep commits focused. Pull requests should explain the behavior change, list validation commands run, link related issues, and include screenshots or short recordings for UI/PWA changes. Call out configuration, migration, or security implications explicitly.

## Security & Configuration Tips

Use local `.env` values for credentials and admin bcrypt configuration; never place real secrets in source, tests, or commits. Preserve the application’s encryption, CSP, and referrer-policy protections when changing handlers or static assets. Run `just vuln` for dependency vulnerability scanning when modifying dependencies.
