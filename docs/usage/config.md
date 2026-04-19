# Configuration reference

fmlocal is configured through a single YAML file, passed via the `-config` flag (default: `config.yaml`). This document describes every field in every section.

## Full example

```yaml
server:
  awsApiPort: 9080
  webUIPort: 9081
  region: us-east-1
  accountId: "000000000000"
  tickInterval: 500ms
  logLevel: info

matchmakingConfigurations:
  - name: default
    ruleSetName: 1v1
    requestTimeoutSeconds: 60
    acceptanceRequired: false
    backfillMode: MANUAL
    flexMatchMode: STANDALONE
    notificationTargets: [localSQS]

  - name: accept
    ruleSetName: 1v1-accept
    requestTimeoutSeconds: 120
    acceptanceRequired: true
    acceptanceTimeoutSeconds: 30
    backfillMode: MANUAL
    flexMatchMode: STANDALONE
    notificationTargets: [localSQS]

ruleSets:
  - name: 1v1
    path: rulesets/1v1.json
  - name: 1v1-accept
    path: rulesets/1v1-accept.json

publishers:
  - id: localSQS
    kind: sqs_eventbridge
    enabled: true
    queueUrl: http://elasticmq:9324/000000000000/fmlocal-events
    awsEndpoint: http://elasticmq:9324
    awsRegion: us-east-1
    accessKey: x
    secretKey: x
    onlyEvents: []
```

## `server`

Global server settings.

| Field | Type | Default | Description |
|---|---|---|---|
| `awsApiPort` | int | — | **Required.** Port for the GameLift-compatible JSON-RPC endpoint. Must differ from `webUIPort`. |
| `webUIPort` | int | — | **Required.** Port for the operator Web UI. |
| `region` | string | `us-east-1` | AWS region embedded in generated ARNs and event envelopes. |
| `accountId` | string | `000000000000` | AWS account ID embedded in generated ARNs and event envelopes. |
| `tickInterval` | duration | `1s` | How often the matchmaker advances. Accepts Go duration strings (`500ms`, `1s`, …). Lower values reduce match latency at the cost of more CPU. |
| `logLevel` | string | `info` | Log verbosity. One of `debug`, `info`, `warn`, `error`. See [Logging](logging.md). |

## `matchmakingConfigurations`

List of matchmaking configurations fmlocal serves. Each entry maps to a GameLift `MatchmakingConfiguration`.

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | string | — | **Required.** Unique name. Used as the `ConfigurationName` in API calls and as the last segment of the generated ARN. |
| `ruleSetName` | string | — | **Required.** Must match a name declared in `ruleSets`. |
| `requestTimeoutSeconds` | int | `0` | Maximum number of seconds fmlocal waits to form a match before timing the ticket out. `0` means no timeout. |
| `acceptanceRequired` | bool | `false` | When `true`, matched players must call `AcceptMatch` before the match is finalized. |
| `acceptanceTimeoutSeconds` | int | `0` | Seconds players have to accept or reject a proposal. Only relevant when `acceptanceRequired` is `true`. |
| `backfillMode` | string | `MANUAL` | Backfill mode. Accepted values: `MANUAL`, `AUTOMATIC`. fmlocal does not implement backfill; the field is stored and returned as-is in API responses. |
| `flexMatchMode` | string | `STANDALONE` | Only `STANDALONE` is supported. fmlocal rejects any other value at startup. |
| `notificationTargets` | []string | `[]` | Ordered list of publisher IDs (declared in `publishers`) that receive lifecycle events for this configuration. Multiple IDs fan out. |

## `ruleSets`

FlexMatch rule sets loaded from disk at startup. The file content is fed verbatim to the flexi matchmaking engine.

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | string | — | **Required.** Unique name. Referenced by `matchmakingConfigurations[*].ruleSetName`. |
| `path` | string | — | **Required.** Path to the JSON rule set file. Relative paths are resolved relative to the config file's directory. |

## `publishers`

Outbound adapters that deliver lifecycle events to external listeners. For the wire payload format, delivery semantics, and how events are selected, see [Event publishers](../feature/publishers.md).

### Common fields

| Field | Type | Default | Description |
|---|---|---|---|
| `id` | string | — | **Required.** Unique identifier. Referenced by `matchmakingConfigurations[*].notificationTargets`. |
| `kind` | string | — | **Required.** Publisher type. One of `sns_http`, `sqs_eventbridge`. |
| `enabled` | bool | `false` | When `false`, the publisher is declared but not instantiated. Matchmaking configurations that reference a disabled publisher are rejected at startup. |
| `onlyEvents` | []string | `[]` | Optional allowlist of event names. When non-empty, only events whose name is in this list are forwarded; others are silently dropped. Valid names: `MatchmakingSearching`, `PotentialMatchCreated`, `AcceptMatch`, `AcceptMatchCompleted`, `MatchmakingSucceeded`, `MatchmakingFailed`, `MatchmakingTimedOut`, `MatchmakingCancelled`. |

### `kind: sns_http`

Posts events to an HTTP endpoint in the SNS notification envelope format.

| Field | Type | Required | Description |
|---|---|---|---|
| `url` | string | ✅ | HTTP endpoint that receives POST requests. |

### `kind: sqs_eventbridge`

Sends events to an SQS queue as an EventBridge-shaped message body.

| Field | Type | Required | Description |
|---|---|---|---|
| `queueUrl` | string | ✅ | SQS queue URL. |
| `awsEndpoint` | string | — | Custom SQS endpoint URL. Used to target ElasticMQ or another local SQS-compatible backend. |
| `awsRegion` | string | — | AWS region for the SQS client. Defaults to `us-east-1` when omitted. |
| `accessKey` | string | — | AWS access key ID. Defaults to `x` when omitted (any non-empty string works with ElasticMQ). |
| `secretKey` | string | — | AWS secret access key. Defaults to `x` when omitted. |

## Startup validation

fmlocal validates the config before serving any requests and exits with an error if any of the following are violated:

- `awsApiPort` and `webUIPort` are both required and must differ.
- Each rule set `name` must be unique.
- Each publisher `id` must be unique.
- `sns_http` publishers require `url`; `sqs_eventbridge` publishers require `queueUrl`.
- `onlyEvents` entries must be known event names.
- Each `matchmakingConfigurations[*].ruleSetName` must match a declared rule set.
- `flexMatchMode` must be `STANDALONE`.
- Each `notificationTargets` entry must reference a declared and enabled publisher.
- `logLevel` must be one of `debug`, `info`, `warn`, `error`.
