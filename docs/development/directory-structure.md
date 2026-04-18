# Source directory layout

This document describes every Go package in the repository, top-down.

See [Architecture](architecture.md) for how the layers fit together.

## `cmd/fmlocal/`

Composition root. The only `package main`.

- `main.go` — parse flags, load config, build one `flexi.Matchmaker` per configuration, assemble the application `Service`, load configurations and rule sets into it, wire publishers, launch the AWS API server, the Web UI server, and the ticker; wait for SIGINT/SIGTERM.

## `internal/domain/matchmaking/`

The matchmaking bounded context. No dependencies on HTTP, AWS SDKs, or infrastructure; depends only on `flexi` (whose types are aliased as domain-level `Player` / `Attribute` / `Attributes`) and the standard library.

| File              | Purpose                                              |
|-------------------|------------------------------------------------------|
| `ids.go`          | Typed identifiers: `TicketID`, `PlayerID`, `MatchID`, `ConfigurationName`, `RuleSetName`. |
| `player.go`       | Type aliases to flexi's `Player`, `Attribute`, `Attributes`, `AttributeKind`. |
| `configuration.go`| `Configuration`, `FlexMatchMode`, `BackfillMode`.   |
| `ruleset.go`      | `RuleSet` (name + ARN + raw JSON body).             |
| `status.go`       | `TicketStatus` enum and its state-machine (`canTransitionTo`). |
| `ticket.go`       | `Ticket` aggregate: fields, constructors, transition methods, event recording, `RebuildTicket` for persistence rehydration. |
| `match.go`        | `Match` and `Proposal` value objects.               |
| `events.go`       | `Event` interface and the eight concrete event types. |
| `errors.go`       | Sentinel errors: `ErrInvalidTransition`, `ErrTicketNotFound`, `ErrPlayerNotInTicket`, `ErrTicketAlreadyExists`. |
| `ticket_test.go`  | Unit tests for the aggregate's transitions.         |

## `internal/app/matchmaking/`

Application service that orchestrates the domain + engine + publishers and owns ticket / configuration / rule-set state. One file per use case.

| File                | Purpose                                            |
|---------------------|----------------------------------------------------|
| `service.go`        | `Service` struct with in-memory maps, `Load*` and `Get/List/SaveTicket` accessors, publisher lookup, event dispatch. |
| `commands.go`       | Command/Query DTOs crossing the inbound boundary.  |
| `errors.go`         | `ErrInvalidCommand`.                               |
| `engine_resolver.go`| `EngineResolver` interface + `StaticEngineResolver` (one flexi engine per configuration). |
| `registry.go`       | Configuration/RuleSet list queries.                |
| `start.go`          | `StartMatchmaking` use case.                        |
| `stop.go`           | `StopMatchmaking` use case.                         |
| `accept.go`         | `AcceptMatch` use case.                             |
| `describe.go`       | `DescribeMatchmaking` query.                        |
| `tick.go`           | `Tick`: advances the engine, drives state transitions, publishes events. |
| `ticker.go`         | Goroutine that calls `Tick` on a fixed interval for every configuration. |
| `service_test.go`   | Unit tests for the service with fake adapters.      |

## `internal/app/ports/`

Side-effecting interfaces the application service depends on. In-process state (tickets, configurations, rule sets) lives on `Service` directly and has no port.

| File              | Port(s)                                                 |
|-------------------|---------------------------------------------------------|
| `publisher.go`    | `EventPublisher`.                                       |
| `clock.go`        | `Clock`.                                                |
| `idgen.go`        | `IDGenerator`.                                          |

## `internal/app/defaults/`

Default implementations of the stateless ports above.

### `idgen/`

| File             | Purpose                                  |
|------------------|------------------------------------------|
| `idgen.go`       | UUIDv4 `IDGenerator` and `Sequence` for tests. |
| `idgen_test.go`  | Smoke test.                              |

### `sysclock/`

| File              | Purpose                                     |
|-------------------|---------------------------------------------|
| `clock.go`        | `System` clock (`time.Now()`) and `Fake` clock for tests. |
| `clock_test.go`   | Sanity check.                               |

## `internal/infrastructure/`

Outbound adapters. Currently contains only event publishers.

### `notification/`

Event publisher adapters.

| File                        | Purpose                                           |
|-----------------------------|---------------------------------------------------|
| `envelope.go`               | EventBridge envelope + shared payload types.      |
| `translator_test.go`        | Tests for translating domain events into payloads. |
| `sns_http.go`               | `NewSNSHTTPPublisher`: posts SNS-shaped JSON.     |
| `sqs_eventbridge.go`        | `NewSQSEventBridgePublisher`: sends to SQS.       |
| `multi.go`                  | `NewMulti`: fan-out publisher + `Noop`.           |
| `publishers_test.go`        | Integration tests against fake HTTP/SQS servers.  |
| `mocks/`                    | Generated mocks for HTTP doer and SQS client.     |

## `internal/system/`

Bootstrap-time concerns. Imported only by `cmd/fmlocal`; the domain and the application service never see these packages.

### `configfile/`

| File            | Purpose                                                |
|-----------------|--------------------------------------------------------|
| `schema.go`     | YAML-tagged parse structs (`document`, `serverSection`, …). |
| `loader.go`     | `LoadFile`, defaulting, rule-set resolution, cross-reference validation, `Loaded` materialization. |
| `loader_test.go`| Tests covering defaults and validation errors.         |

## `internal/interfaces/`

Inbound adapters.

### `awsapi/`

GameLift JSON 1.1 JSON-RPC server.

| File                         | Purpose                                           |
|------------------------------|---------------------------------------------------|
| `server.go`                  | `Server` struct, `Handler`, `Run` (start + graceful shutdown). |
| `dispatcher.go`              | `X-Amz-Target` routing and JSON decode.           |
| `dto.go`                     | Wire DTOs for every supported operation.          |
| `convert.go`                 | DTO ↔ domain/flexi conversion helpers.            |
| `errors.go`                  | `APIError` + GameLift-compatible error codes + `translateDomainError`. |
| `handlers_matchmaking.go`    | Handlers for `Start/Stop/Describe/AcceptMatch`.   |
| `handlers_registry.go`       | Handlers for configurations, rule sets, and `ValidateMatchmakingRuleSet`. |
| `convert_test.go`            | Conversion round-trip tests.                      |
| `server_test.go`             | End-to-end server tests with a real HTTP server.  |

### `webui/`

Read-only operator UI rendered with `html/template`.

| File               | Purpose                                                  |
|--------------------|----------------------------------------------------------|
| `server.go`        | `Server` struct, route table, template parsing (embedded). |
| `templates/`       | HTML templates embedded via `go:embed`.                   |
| `server_test.go`   | Smoke tests over the rendered HTML.                       |

## `test/`

Cross-cutting tests that exercise the whole stack.

| File                               | Purpose                                    |
|------------------------------------|--------------------------------------------|
| `e2e/matchmaking_test.go`          | Full matchmaking lifecycle via `awsapi`.   |
| `e2e/pipeline_test.go`             | End-to-end notification pipeline.          |
| `e2e/sqs_eventbridge_test.go`      | SQS publisher against ElasticMQ (testcontainers). |

## `testdata/`

Fixtures used by tests and by `go run`.

- `config/sample.yaml` — sample fmlocal config with an SNS HTTP publisher.
- `rulesets/1v1.json` and `rulesets/1v1-accept.json` — FlexMatch rule sets.

## `deploy/local/`

Configuration consumed by `compose.yaml`. See [Running locally](../usage/local.md).

- `config.yaml` — fmlocal config pointing at the ElasticMQ container.
- `elasticmq.conf` — HOCON for ElasticMQ.
- `rulesets/` — rule sets mounted into the fmlocal container.
