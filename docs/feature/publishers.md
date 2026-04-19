# Event publishers

fmlocal emits a lifecycle event every time a matchmaking ticket changes state. Publishers are the outbound adapters that deliver those events to external listeners so a FlexMatch client can observe progress the same way it would against real AWS GameLift. This page is the single reference for which publishers exist, what they deliver, and how they behave.

## Role in the pipeline

When a ticket transitions, the `Ticket` aggregate records a `matchmaking.Event` into its internal buffer. After every mutation the application service (`Service.dispatchEvents`) drains that buffer and hands each event to the publisher bound to the ticket's configuration. The publisher owns the translation from the domain event into an AWS-flavored wire payload and the transport to the configured destination.

Publishers are wired per matchmaking configuration. Each `matchmakingConfigurations[*]` entry lists zero or more publisher IDs under `notificationTargets`; at startup the composition root builds the concrete publishers declared in the top-level `publishers:` section and binds them by ID. A configuration with multiple targets fans out (see below).

## Wire payload

Regardless of the transport, the body delivered to a listener is the same AWS EventBridge envelope GameLift itself publishes for FlexMatch:

```json
{
  "version": "0",
  "id": "<uuid>",
  "detail-type": "GameLift Matchmaking Event",
  "source": "aws.gamelift",
  "account": "000000000000",
  "time": "2026-04-19T10:00:00Z",
  "region": "us-east-1",
  "resources": [
    "arn:aws:gamelift:us-east-1:000000000000:matchmakingconfiguration/<name>"
  ],
  "detail": {
    "type": "MatchmakingSucceeded",
    "tickets": [ { "ticketId": "...", "players": [ { "playerId": "..." } ] } ],
    "matchId": "..."
  }
}
```

`detail.type` is the domain event name. fmlocal emits eight of them, matching the events real GameLift produces:

| Event name               | Emitted when                                                              |
|--------------------------|---------------------------------------------------------------------------|
| `MatchmakingSearching`   | `StartMatchmaking` creates a ticket and hands it to the engine.           |
| `PotentialMatchCreated`  | The engine proposes a match and tickets move to `PLACING` (or `REQUIRES_ACCEPTANCE` when acceptance is required). |
| `AcceptMatch`            | A player records `ACCEPT` / `REJECT` via `AcceptMatch`.                    |
| `AcceptMatchCompleted`   | All players have responded (or the acceptance window timed out) and the proposal's acceptance phase is closed. |
| `MatchmakingSucceeded`   | A proposal is finalized; every involved ticket moves to `COMPLETED`.      |
| `MatchmakingFailed`      | Acceptance failed for at least one player and the proposal collapsed.      |
| `MatchmakingTimedOut`    | `requestTimeoutSeconds` elapsed before the ticket could match, or the acceptance timeout elapsed. |
| `MatchmakingCancelled`   | `StopMatchmaking` cancelled the ticket.                                    |

The detail fields populated per event type are implemented in `internal/infrastructure/notification/envelope.go`.

## Supported publishers

### `sns_http`

Posts the EventBridge envelope to an HTTP endpoint, wrapped in an SNS `Notification` message — mimicking an SNS topic with an HTTP subscription.

- **Use case.** Local development where a test listener exposes an HTTP endpoint and wants SNS-shaped POSTs. Convenient because it needs no AWS-compatible backend.
- **Framing.** Outer body is `application/json` with SNS fields (`Type`, `MessageId`, `TopicArn`, `Message`, `Timestamp`, `Signature`, …). The inner `Message` string is the EventBridge envelope as JSON.
- **SNS headers.** `x-amz-sns-message-type`, `x-amz-sns-message-id`, and `x-amz-sns-topic-arn` are set so a subscriber written for real SNS can branch on them.
- **Signature.** `Signature` is the literal string `fmlocal-unsigned` — fmlocal does not implement SNS message signing. Listeners that validate signatures must be put in a development mode.
- **TopicArn.** Hardcoded to the placeholder `arn:aws:sns:local:000000000000:fmlocal` and not configurable. fmlocal is not a real SNS topic; the field is present only so consumers that inspect or log it see a well-formed value.
- **Success criteria.** HTTP status `<400`. Any `4xx` / `5xx` is treated as a failure (logged, not retried).
- **Config keys.** Required: `url`.

### `sqs_eventbridge`

Sends the EventBridge envelope to an SQS queue as the message body — mimicking an EventBridge rule targeting SQS.

- **Use case.** Production-like setup where consumers read from SQS. The bundled `docker compose` stack pairs fmlocal with ElasticMQ and uses this publisher.
- **Framing.** The SQS `MessageBody` is the EventBridge envelope as JSON. No SNS-style outer wrapping.
- **Backend.** Any SQS-compatible endpoint works. `awsEndpoint` lets the publisher target a local ElasticMQ instead of real AWS SQS. `awsRegion`, `accessKey`, `secretKey` are forwarded to the AWS SDK but ElasticMQ accepts any value.
- **Success criteria.** `SendMessage` must not return an error. No response content is validated.
- **Config keys.** Required: `queueUrl`. Optional: `awsEndpoint`, `awsRegion`, `accessKey`, `secretKey`.

## Fan-out and defaults

- **Multiple publishers per configuration.** When a matchmaking configuration lists several `notificationTargets`, the composition root wraps them in a fan-out publisher (`notification.Multi`). Each event is forwarded to every child; per-child errors are joined but do not short-circuit the fan-out.
- **No publisher configured.** A configuration with no `notificationTargets` gets `notification.Noop`: events are still recorded on the ticket, but delivery is silently skipped. This is also what tests use when they don't care about publication.
- **Cross-configuration isolation.** Publisher bindings are per configuration, so a stray consumer on one configuration's SQS queue never sees events from another configuration.

## Delivery semantics

- **Best-effort, fire-and-forget.** `Service.dispatchEvents` catches and logs publisher errors; it never rolls back the already-applied ticket transition. Real GameLift decouples matchmaking from notification the same way, so clients must not assume that a missing event means a missing state change — they should reconcile by polling `DescribeMatchmaking`.
- **At-most-once.** fmlocal does not retry failed publishes. If an HTTP listener returns 500 or `SendMessage` fails, the event is dropped and a warning is logged. This is deliberate: delivery retry, dedup, and back-pressure are consumer-side concerns that differ per deployment.
- **Per-ticket ordering.** Events for a given ticket are dispatched in the order the aggregate recorded them, within the serialized `dispatchEvents` call. Across tickets and configurations, order is not guaranteed.
- **No authentication.** `sns_http` emits the stub signature `fmlocal-unsigned`. `sqs_eventbridge` passes credentials to the AWS SDK but fmlocal itself does not authenticate callers. **Do not expose fmlocal or its publishers to the public internet.**

## Configuration reference

YAML schema (`config.yaml`):

```yaml
matchmakingConfigurations:
  - name: default
    ruleSetName: 1v1
    notificationTargets: [localSQS, httpSink]   # publisher IDs; fan-out if >1

publishers:
  - id: localSQS
    kind: sqs_eventbridge
    enabled: true
    queueUrl: http://elasticmq:9324/000000000000/fmlocal-events
    awsEndpoint: http://elasticmq:9324
    awsRegion: us-east-1
    accessKey: x
    secretKey: x
    onlyEvents:                       # optional; allowlist of event names
      - MatchmakingSucceeded
      - MatchmakingFailed

  - id: httpSink
    kind: sns_http
    enabled: true
    url: http://host.docker.internal:9000/sns
```

Startup validation (`internal/system/configfile`) rejects: duplicate publisher IDs, unknown or missing `kind`, `sns_http` without `url`, `sqs_eventbridge` without `queueUrl`, matchmaking configurations referencing unknown or disabled publisher IDs, and `onlyEvents` entries that are not in the canonical event list (see the event catalog above).

`enabled: false` keeps the entry declared but skips instantiation — useful when toggling listeners without editing `notificationTargets`.

### Filtering events per publisher

`onlyEvents` is an optional allowlist of event names (from the catalog above) the publisher will emit. Omitted or empty means "forward everything" — unchanged from prior behavior. Typical use cases:

- An SQS consumer that only cares about terminal outcomes: `onlyEvents: [MatchmakingSucceeded, MatchmakingFailed, MatchmakingTimedOut, MatchmakingCancelled]`.
- A noisy HTTP sink used for lifecycle debugging that should skip per-player `AcceptMatch` spam: everything except `AcceptMatch`.

Filtering is implemented as a decorator (`notification.Filtered`) wrapping the concrete publisher; it works identically for `sns_http` and `sqs_eventbridge`. A dropped event is silently skipped — no publisher call, no log line.

## Inspecting events locally

If you run with `deploy/local/config.yaml`, every lifecycle transition lands in the `fmlocal-events` queue on ElasticMQ:

```sh
aws sqs receive-message \
  --endpoint-url http://localhost:9324 \
  --region us-east-1 \
  --queue-url http://localhost:9324/000000000000/fmlocal-events \
  --max-number-of-messages 10 \
  --wait-time-seconds 1
```

If you configured an `sns_http` publisher instead, run any local HTTP listener on the configured `url` and you will receive SNS-shaped POSTs whose `Message` field is the EventBridge envelope.

## Extending

A new publisher kind is a new adapter under `internal/infrastructure/notification/` that implements `ports.EventPublisher.Publish(ctx, mm.Event) error`. To make it usable from YAML, add a `PublisherKind` constant plus the required-field validation in `internal/system/configfile/loader.go`, and wire instantiation into `cmd/fmlocal/main.go:buildPublishers`. Prefer reusing `notification.Translator` so the `detail` payload shape stays identical across kinds.
