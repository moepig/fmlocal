# Architecture

fmlocal follows a hexagonal (ports-and-adapters) layout with a DDD-flavored domain at the center. The goal is that the matchmaking domain can be reasoned about and tested without reference to HTTP, AWS SDKs, or even the `flexi` matchmaking engine, while adapters on the outside speak whatever wire protocols are required. The application service owns in-memory state directly — tickets, configurations, and rule sets live in maps on `Service`, because fmlocal never persists anything.

```
                     +-------------------------+
 AWS SDK / CLI  -->  |   interfaces/awsapi     |  inbound adapter (JSON-RPC)
                     +-----------+-------------+
                                 |
 Browser        -->  | interfaces/webui |      inbound adapter (HTML)
                     +-----------+-------------+
                                 |
                     +-----------v-------------+
                     |   app/matchmaking       |  application service
                     |   + app/ports           |  (holds state + ports for
                     |   + app/defaults        |   side-effecting dependencies)
                     +-----------+-------------+
                                 |
                     +-----------v-------------+
                     |  domain/matchmaking     |  aggregates, value objects,
                     |                         |  events, state machine
                     +-------------------------+
                                 ^
                                 |  (implements ports)
                     +-----------+-------------+
                     |   infrastructure/...    |  outbound adapters:
                     | notification            |  SNS/SQS publishers
                     +-------------------------+

                     +-------------------------+
                     |   system/...            |  bootstrap concerns
                     | configfile              |  (YAML loading)
                     +-------------------------+
```

## Layers

### Domain (`internal/domain/matchmaking`)

The vocabulary of matchmaking: `Ticket` aggregate (state machine + event recorder), value objects (`TicketID`, `ConfigurationName`, `RuleSetName`, `PlayerID`, `MatchID`), `Configuration`, `RuleSet`, `TicketStatus` with enforced transitions, and the `Event` interface and its eight concrete event types.

The domain owns two invariants:

1. **State transitions.** `TicketStatus.canTransitionTo` enumerates the allowed edges. `Ticket` never moves to a status that the state machine rejects. The application layer cannot bypass this — every mutation goes through an intent-revealing method (`AssignToProposal`, `MoveToPlacing`, `MarkFailed`, …).
2. **Event emission.** Each transition records the corresponding domain event into an internal buffer. The application layer pulls events after a mutation and hands them to a publisher; the domain does not know what a publisher is.

`Player`, `Attribute`, `Attributes` are Go type aliases to the corresponding types in the [flexi] engine. fmlocal uses flexi end-to-end, so there is no separate domain `Player` struct — the alias keeps call sites domain-flavored without paying a translation cost.

### Application (`internal/app/matchmaking`, `internal/app/ports`, `internal/app/defaults`)

Thin use cases that orchestrate the domain, the matchmaking engine, and the publishers. Each file corresponds to one operation: `start.go`, `stop.go`, `accept.go`, `describe.go`, `tick.go`, plus the shared `Service` struct in `service.go`.

`Service` holds ticket, configuration, and rule-set state in maps guarded by a `sync.RWMutex`, and exposes `Load*` methods (populated at startup from the config file) and `Get` / `List` / `SaveTicket` accessors. There is no repository abstraction — fmlocal only ever runs in one process with no persistence, so an interface on top of a map was pure indirection.

`Service.Tick` is the engine driver: on every ticker fire it asks flexi for newly-formed proposals and finished matches, then advances the affected tickets through the domain transitions and publishes the resulting events.

Ports (`internal/app/ports`) are the side-effecting interfaces the application still depends on: `EventPublisher`, `Clock`, `IDGenerator`. The default implementations live under `internal/app/defaults/` (`idgen`, `sysclock`); production publishers live under `infrastructure/notification`.

### Interfaces (`internal/interfaces/{awsapi,webui}`)

Inbound adapters. They translate transport-specific inputs into application commands and application results into transport-specific outputs.

`awsapi` implements a subset of the GameLift JSON 1.1 protocol: it dispatches on `X-Amz-Target`, decodes the wire DTO, calls the application service, encodes the response, and maps domain errors to GameLift error codes. Wire DTOs live in `dto.go`; the DTO ↔ domain/flexi conversion helpers live in `convert.go`.

`webui` serves a read-only operator view using `html/template` with templates embedded via `go:embed`. It is given a `*appmm.Service` and reads through the service's public accessors.

### Infrastructure (`internal/infrastructure/...`)

Outbound adapters that implement ports.

- `notification` — event publishers. Implements the `EventPublisher` port with the `sns_http` and `sqs_eventbridge` kinds, plus `Multi` for fan-out and `Noop` for unconfigured configurations. `translator.go` projects domain events into the EventBridge envelope shared by both kinds. For the wire payload, delivery semantics, and configuration reference, see [Event publishers](../feature/publishers.md).

### Application defaults (`internal/app/defaults/...`)

Default implementations of the simple, stateless ports that the service needs regardless of deployment.

- `idgen` — UUIDv4 `IDGenerator` (plus a deterministic `Sequence` for tests).
- `sysclock` — `time.Now()`-backed `Clock` (plus a `Fake` clock for tests).

### System (`internal/system/...`)

Bootstrap-time concerns that the domain and the application service never touch. Only the composition root imports them.

- `configfile` — loads YAML, resolves rule set file paths, validates cross-references, and materializes domain `Configuration` / `RuleSet` values plus publisher settings.

### Composition root (`cmd/fmlocal`)

`main.go` wires everything together: parse flags, load the config file, build one `flexi.Matchmaker` per configuration, construct the application `Service`, call `Service.LoadConfigurations` / `LoadRuleSets` with the materialized config, build publishers, start the AWS API server, the Web UI server, and the ticker, and wait for a signal.

## Lifecycle of a match

A typical `default`-configuration path:

1. Client calls `StartMatchmaking`. `awsapi.handleStartMatchmaking` decodes the wire DTO, converts `Player` attributes from AWS's `AttributeValue` union into `flexi.Attributes`, and calls `Service.StartMatchmaking`.
2. The service looks up the `Configuration`, asks the engine resolver for the `flexi.Matchmaker`, constructs a new `Ticket` aggregate (which emits `MatchmakingSearching`), enqueues the ticket into flexi, and saves it.
3. The ticker fires `Service.Tick`. It asks the engine for proposals and completed matches, then drives the corresponding tickets through `AssignToProposal` / `MoveToPlacing` / `Complete`.
4. Each transition records a domain event; after the mutation the service pulls the events off the ticket and invokes the publisher registered for the configuration.
5. The publisher (SNS HTTP or SQS EventBridge) translates the domain event into wire bytes and sends it.
6. Clients observe the status change either by polling `DescribeMatchmaking` or by consuming published events.

## Error handling

Application-layer errors fall into three buckets:

- **Domain errors** (`mm.ErrInvalidTransition`, `mm.ErrTicketNotFound`, `mm.ErrPlayerNotInTicket`, `mm.ErrTicketAlreadyExists`). The awsapi adapter maps these to GameLift error codes.
- **Application errors** (`appmm.ErrInvalidCommand`, unknown configuration). Also mapped to GameLift error codes.
- **Infrastructure errors** (engine failures, unknown publisher). Logged and surfaced as `InternalServiceException`.

Publishers never fail the caller: `Service.dispatchEvents` logs and continues, because publishing is best-effort and partial failure should not roll back an already-applied state transition.

## What fmlocal deliberately does not do

- **Game session placement.** `flexMatchMode: WITH_QUEUE` and `StartMatchBackfill` / `StopMatchBackfill` are unsupported; only `STANDALONE` matchmaking is implemented.
- **Persistence.** Everything is in-memory. Restart clears tickets.
- **Authentication.** Credentials are ignored. fmlocal is a development tool; do not expose it publicly.

[flexi]: https://github.com/moepig/flexi
