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

## Configuration

For the full list of config fields and their defaults, see [Configuration reference](../usage/config.md). For log level settings, see [Logging](../usage/logging.md).
