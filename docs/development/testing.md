# Running tests locally

fmlocal has three flavors of Go tests:

1. **Unit tests** — pure, fast, no external dependencies. Cover the domain aggregate, the configfile loader, the notification translator, and the conversion helpers.
2. **Adapter tests** — exercise one adapter end-to-end against in-process fakes (an `httptest.Server` for SNS, a mocked SQS client via `go.uber.org/mock`).
3. **End-to-end tests** — live under `test/e2e/`. They stand up the AWS API server (`awsapi`) in-process and drive it with the real AWS SDK (`aws-sdk-go-v2/service/gamelift`). `sqs_eventbridge_test.go` additionally launches a real `softwaremill/elasticmq-native` container via `testcontainers-go`, which requires Docker.

## Prerequisites

- Go matching the toolchain declared in `go.mod`.
- `go.sum` in sync: run `go mod download` on first clone.
- For the testcontainers-backed e2e test only: Docker Engine, reachable via the current user (no `sudo`).

## Run everything

```sh
go test ./...
```

This includes the testcontainers test. Expect ~60-90 seconds cold (Docker pulls the ElasticMQ image on the first run).

## Run everything except the testcontainers test

```sh
go test -short ./...
```

The SQS-EventBridge e2e test respects `testing.Short()` and is skipped.

## Run only unit-style tests

Target the fast packages directly:

```sh
go test \
  ./internal/domain/... \
  ./internal/app/... \
  ./internal/system/configfile/... \
  ./internal/infrastructure/notification/... \
  ./internal/interfaces/awsapi/...
```

## Run a single test

```sh
go test -run TestE2E_TwoTicketsMatchAndComplete ./test/e2e/...
go test -run TestTicket_ /some_regex/ ./internal/domain/matchmaking/...
```

`-run` accepts a regex matched against `TestXxx` names.

## Race detector

```sh
go test -race ./...
```

Recommended before opening a PR — the ticker + service + publisher path is concurrent.

## Verbose output

```sh
go test -v ./test/e2e/...
```

`slog` output from the in-process server is written to the test's stderr.

## Coverage

```sh
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Mocks

Adapter-level mocks are generated with `go.uber.org/mock`. The relevant packages have `go:generate` directives:

```sh
go generate ./...
```

Run this after changing a port interface (for example, adding a method to `ports.EventPublisher`). Generated files live under `*/mocks/`.

## Tests that hit Docker

`test/e2e/sqs_eventbridge_test.go` uses `testcontainers-go` to start ElasticMQ. Behavior on common environments:

- **Linux with Docker installed** — works out of the box.
- **macOS with Docker Desktop** — works. First run pulls the image.
- **WSL2** — Docker must be reachable at the default UNIX socket. `docker info` from inside the WSL shell should succeed before running the tests.
- **No Docker** — use `go test -short ./...` to skip.

If Docker is present but on a non-default socket, export `DOCKER_HOST=unix:///path/to/docker.sock` before running `go test`.

## CI tips

For CI environments without Docker-in-Docker, use:

```sh
go vet ./...
go test -race -short ./...
```

…and run the testcontainers-backed tests on a runner that does have Docker.

## Adding a new test

- **Domain behavior** — add to `internal/domain/matchmaking/ticket_test.go` or create a sibling `*_test.go`. No mocks needed; construct a `Ticket` directly and assert on emitted events.
- **Use case** — add to `internal/app/matchmaking/service_test.go`. Build a `Service`, call `LoadConfigurations` / `LoadRuleSets`, and wire fakes for the publisher/clock/idgen ports; the existing test file has examples.
- **Adapter** — add next to the adapter. Keep external calls behind an interface and inject an `httptest.Server` or a gomock fake.
- **End-to-end** — add to `test/e2e/`. Reuse the `startServer` helper from `matchmaking_test.go` when possible.
