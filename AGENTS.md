# AGENTS.md

Guidance for coding agents working on dviz. Read README.md for what the app does.

## Layout

```
cmd/dviz/                 main: no flags, resolves config, runs app.Run
internal/config/          dviz.yml search order, parsing/validation, directory watcher
internal/app/             supervisor: applies config at runtime (hosts, listen address)
internal/docker/          the only Docker gateway
  reader.go               docker.Reader — every daemon read, sanitized before returning
  sanitize.go             strips env values, secret/config data, driver/log options, healthcheck output
  snapshot.go             Fetch + Build: daemon state -> model.Graph (pure)
  reach.go                network reachability rules + container peers
  details.go              curated per-kind DTOs for the details endpoint
  logs.go, stats.go       log/stat streams
  ssh.go                  ssh:// hosts: in-process SSH client forwarding the remote Docker socket
internal/model/           graph types and Diff
internal/hub/             per-host hub (events -> debounce -> snapshot -> diff -> subscribers), registry
internal/server/          read-only HTTP API (JSON + SSE), Host-header guard, embedded SPA
web/                      Svelte 5 + TypeScript + Vite + 3d-force-graph frontend
  embed.go                go:embed of web/dist; `go generate ./web` builds it
  src/lib/types.ts        mirrors of the Go DTOs
  e2e/                    Playwright tests (real binary, local daemon, dind)
integration/              Go integration tests (real daemons, in-process dviz)
```

## Commands

```sh
npm --prefix web ci && go generate ./web                 # build frontend into web/dist
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" ./cmd/dviz
go vet ./... && gofmt -l .                               # gofmt must print nothing
go test ./...                                            # unit + integration (needs Docker)
npm --prefix web run check && npm --prefix web test       # svelte-check + vitest
npm --prefix web run e2e                                 # Playwright (needs Docker, Chromium)
```

The integration tests and e2e tests create fixtures labelled `dviz.test.run` / `dviz.e2e.run`. They also start a privileged `docker:dind` container (second tcp+TLS host) and an Alpine `sshd` container that exposes the local socket (SSH host). They clean up after themselves.

## Hard rules

- **Never use CGO.** Every build sets `CGO_ENABLED=0`, and the binary must stay statically linked. Only add pure-Go dependencies.
- **Configure only through `dviz.yml`.** The file is mandatory; never add a built-in fallback config. Don't add CLI flags or environment-variable configuration. New settings go into `internal/config`, are validated there, are applied live by the supervisor, and are documented in the README's configuration reference in the same change.
- **Keep the app read-only.** Never add endpoints or Reader methods that change daemon state. Every route is GET.
- **Route every Docker read through `docker.Reader`.** Sanitize every returned value before it leaves `internal/docker`.
  - When you add a Reader method or start using a new field, extend `sanitize.go`.
  - Extend `integration/sanitize_test.go` too, so a planted secret would be caught.
  - Never return raw inspect JSON (`Raw` fields) or image history.
- **Tests:**
  - Prefer integration tests: real daemons for Go, the real binary plus a browser for the frontend.
  - Don't write tests that only exercise framework or library behavior.
  - Unit tests (vitest) are for non-trivial logic of our own.
- **Pin exact versions** for npm packages, Docker base images and Go modules.
- **Keep the DTOs in sync.** When a Go DTO changes, update `web/src/lib/types.ts` in the same change.
- **Never expose environment variables.** Not even their names: no DTO field, no UI. dviz shows infrastructure, not what runs inside containers. The read-boundary stripping in `sanitize.go` stays as an extra safety layer.
- **Never discover credentials automatically.** SSH hosts authenticate only with the configured `ssh.method` (`password`, `agent` or `key`). Don't scan `~/.ssh` or fall back to other methods.
- **Use Conventional Commits** (`feat(scope): …`, `fix: …`, `test: …`, `docs: …`, `build: …`, `ci: …`, `chore: …`, `refactor: …`).

## Frontend notes

- **Store nodes keep their identity.** `GraphStore` mutates node objects in place so 3d-force-graph keeps their positions.
  - Derived UI values that must react to node changes copy the node (`{ ...n }`) after reading `store.version`.
- **One graph store per host.** Stores are kept while the host exists, so switching scenes preserves layouts.
- **Visible state has DOM hooks.** `data-testid` attributes are used by the e2e tests. Keep them stable when refactoring.
