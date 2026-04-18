# Running fmlocal locally

fmlocal can be started in two ways:

1. **Docker Compose** — the recommended default. Brings up fmlocal together with an ElasticMQ container that acts as an SQS backend for event publishing. Matches the topology fmlocal expects in production-like setups.
2. **Native `go run`** — faster inner-loop iteration. You supply the config file directly.

Both modes use the same YAML configuration schema; only the paths and the publisher endpoints differ.

## Option 1: Docker Compose

Requirements: Docker Engine with Compose v2.

```sh
docker compose up --build
```

The `compose.yaml` at the repository root starts two services:

| Service    | Purpose                                   | Ports        |
|------------|-------------------------------------------|--------------|
| `fmlocal`  | GameLift-compatible API + Web UI          | 9080, 9081   |
| `elasticmq`| SQS-compatible queue backend              | 9324, 9325   |

fmlocal mounts:

- `deploy/local/config.yaml` at `/etc/fmlocal/config.yaml`
- `deploy/local/rulesets/` at `/etc/fmlocal/rulesets/`

ElasticMQ mounts `deploy/local/elasticmq.conf` and pre-creates the `fmlocal-events` queue on startup.

### Endpoints

| Endpoint                                | URL                           |
|-----------------------------------------|-------------------------------|
| AWS GameLift JSON-RPC                   | `http://localhost:9080`       |
| Operator Web UI                         | `http://localhost:9081`       |
| SQS (ElasticMQ)                         | `http://localhost:9324`       |
| ElasticMQ stats UI                      | `http://localhost:9325`       |
| fmlocal health probe                    | `http://localhost:9080/healthz` |

### Verifying the stack is up

```sh
curl -s http://localhost:9080/healthz
# => ok
```

```sh
aws sqs list-queues \
  --endpoint-url http://localhost:9324 \
  --region us-east-1
```

The `fmlocal-events` queue should be listed.

### Stopping

```sh
docker compose down
```

Add `-v` to remove volumes (ElasticMQ holds its queues in memory only by default, so this is usually not needed).

## Option 2: Run natively with `go run`

Requirements: Go matching the toolchain in `go.mod`.

```sh
go run ./cmd/fmlocal -config testdata/config/sample.yaml
```

The default `testdata/config/sample.yaml` uses an SNS-over-HTTP publisher pointing at `http://localhost:9000/sns`. That publisher is enabled by default but fails silently if no listener is present, which is fine for client-only testing.

To publish to ElasticMQ while running natively, start only ElasticMQ from the compose file:

```sh
docker compose up elasticmq
```

Then copy `deploy/local/config.yaml` somewhere writable, change the `queueUrl` / `awsEndpoint` hostnames from `elasticmq` to `localhost`, and point `go run` at it:

```sh
cp deploy/local/config.yaml local.yaml
cp -r deploy/local/rulesets ./rulesets
# edit local.yaml: s/elasticmq/localhost/g
go run ./cmd/fmlocal -config local.yaml
```

## Configuration reference

`config.yaml` has four sections:

```yaml
server:
  awsApiPort: 9080          # required, must differ from webUIPort
  webUIPort: 9081           # required
  region: us-east-1         # default us-east-1
  accountId: "000000000000" # default 000000000000
  tickInterval: 500ms       # default 1s; how often the matchmaker advances

matchmakingConfigurations:  # maps to GameLift MatchmakingConfiguration
  - name: default
    ruleSetName: 1v1
    requestTimeoutSeconds: 60
    acceptanceRequired: false
    backfillMode: MANUAL          # MANUAL or AUTOMATIC
    flexMatchMode: STANDALONE     # only STANDALONE is supported
    notificationTargets: [localSQS]

ruleSets:                   # FlexMatch rule sets loaded from disk
  - name: 1v1
    path: rulesets/1v1.json # resolved relative to the config file's dir

publishers:                 # outbound adapters that emit lifecycle events
  - id: localSQS
    kind: sqs_eventbridge   # or sns_http
    enabled: true
    queueUrl: http://elasticmq:9324/000000000000/fmlocal-events
    awsEndpoint: http://elasticmq:9324
    awsRegion: us-east-1
    accessKey: x
    secretKey: x
```

See [Event publishers](../feature/publishers.md) for the supported publisher kinds, their wire payloads, delivery semantics, and the full field reference. Startup validation also rejects a non-`STANDALONE` `flexMatchMode` and rule sets that are declared but never referenced.

## Logs

fmlocal uses `slog` with the text handler and writes to stderr. Docker Compose streams it to `docker compose logs -f fmlocal`.
