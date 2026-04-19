# Running fmlocal with the GHCR image

Pre-built multi-arch images (`linux/amd64`, `linux/arm64`) are published to the GitHub Container Registry on every release:

```
ghcr.io/moepig/fmlocal:<version>
ghcr.io/moepig/fmlocal:latest
```

This lets you run fmlocal without cloning the repository or installing Go.

## Requirements

- Docker Engine with Compose v2

## Quick start with Docker Compose

Create a working directory and copy the sample configuration files from the repository:

```sh
mkdir fmlocal-stack && cd fmlocal-stack
curl -fsSL https://raw.githubusercontent.com/moepig/fmlocal/main/deploy/local/elasticmq.conf -o elasticmq.conf
mkdir rulesets
curl -fsSL https://raw.githubusercontent.com/moepig/fmlocal/main/deploy/local/rulesets/1v1.json -o rulesets/1v1.json
curl -fsSL https://raw.githubusercontent.com/moepig/fmlocal/main/deploy/local/rulesets/1v1-accept.json -o rulesets/1v1-accept.json
curl -fsSL https://raw.githubusercontent.com/moepig/fmlocal/main/deploy/local/config.yaml -o config.yaml
```

Then create a `compose.yaml` that uses the pre-built image:

```yaml
services:
  elasticmq:
    image: softwaremill/elasticmq-native:1.6.11
    container_name: fmlocal-elasticmq
    ports:
      - "9324:9324"
      - "9325:9325"
    volumes:
      - ./elasticmq.conf:/opt/elasticmq.conf:ro
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "-", "http://localhost:9324/?Action=ListQueues"]
      interval: 5s
      timeout: 3s
      retries: 10

  fmlocal:
    image: ghcr.io/moepig/fmlocal:latest
    container_name: fmlocal
    depends_on:
      elasticmq:
        condition: service_healthy
    ports:
      - "9080:9080"
      - "9081:9081"
    volumes:
      - ./config.yaml:/etc/fmlocal/config.yaml:ro
      - ./rulesets:/etc/fmlocal/rulesets:ro
    command: ["-config", "/etc/fmlocal/config.yaml"]
```

Start the stack:

```sh
docker compose up
```

## Pinning to a specific version

Replace `latest` with the desired version tag, e.g.:

```yaml
image: ghcr.io/moepig/fmlocal:0.3.0
```

Available tags can be found on the [package page](https://github.com/moepig/fmlocal/pkgs/container/fmlocal).

## Endpoints

| Endpoint                | URL                             |
|-------------------------|---------------------------------|
| AWS GameLift JSON-RPC   | `http://localhost:9080`         |
| Operator Web UI         | `http://localhost:9081`         |
| SQS (ElasticMQ)         | `http://localhost:9324`         |
| ElasticMQ stats UI      | `http://localhost:9325`         |
| Health probe            | `http://localhost:9080/healthz` |

## Verifying the stack is up

```sh
curl -s http://localhost:9080/healthz
# => ok
```

```sh
aws gamelift describe-matchmaking-configurations \
  --endpoint-url http://localhost:9080 \
  --region us-east-1
```

## Stopping

```sh
docker compose down
```

## Configuration

Modify `config.yaml` and add rule set JSON files under `rulesets/` to customise matchmaking behaviour. For the full schema, see [Configuration reference](config.md).
