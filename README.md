# fmlocal

A local, self-contained server that emulates the AWS GameLift FlexMatch matchmaking API. It lets you develop and integration-test FlexMatch clients without talking to real GameLift: the AWS SDK points at `http://localhost:9080`, fmlocal runs an in-memory matchmaker (via [flexi]) against your FlexMatch rule sets, and publishes the same lifecycle events to an SNS HTTP topic or an SQS queue (EventBridge envelope) that production would emit.

## What you get

- **GameLift-compatible JSON-RPC endpoint** on port `9080`: `StartMatchmaking`, `StopMatchmaking`, `DescribeMatchmaking`, `AcceptMatch`, `Describe/ListMatchmakingConfigurations`, `Describe/ListMatchmakingRuleSets`, and `ValidateMatchmakingRuleSet`.
- **Web UI** on port `9081`: read-only operator view for rule sets, configurations, and live tickets.
- **Event publishers**: SNS-over-HTTP and SQS (EventBridge-shaped payload).
- **Config-file driven**: YAML describes ports, configurations, rule sets, and publishers. Rule sets are verbatim FlexMatch JSON, fed directly to the engine.

## Quick start

```sh
docker compose up --build
```

This brings up fmlocal together with an ElasticMQ instance acting as the SQS backend. Once healthy:

- AWS API endpoint: `http://localhost:9080`
- Web UI: `http://localhost:9081`
- SQS (ElasticMQ): `http://localhost:9324`
- ElasticMQ stats UI: `http://localhost:9325`

To point an AWS CLI at it:

```sh
aws gamelift describe-matchmaking-configurations \
  --endpoint-url http://localhost:9080 \
  --region us-east-1
```

## Documentation

- Usage
  - [Running with the GHCR image](docs/usage/docker-compose.md)
  - [Configuration reference](docs/usage/config.md)
  - [Logging](docs/usage/logging.md)
- Features
  - [Event publishers](docs/feature/publishers.md)
- Development
  - [Running locally (from source)](docs/development/local.md)
  - [Architecture](docs/development/architecture.md)
  - [Source directory layout](docs/development/directory-structure.md)
  - [Verifying with the AWS CLI](docs/development/aws-cli.md)
  - [Running tests locally](docs/development/testing.md)

## Repository layout

```
cmd/fmlocal/        Composition root (main)
internal/
  app/              Application service (holds in-memory state), ports, default port implementations
  domain/           Domain model (aggregates, value objects, events)
  infrastructure/   Outbound adapters (publishers)
  system/           Bootstrap-only packages (config loader)
  interfaces/       Inbound adapters (AWS API, Web UI)
test/e2e/           End-to-end tests
testdata/           Sample YAML config and rule sets used by tests
deploy/local/       Config and rule sets consumed by docker compose
```

## License

See `LICENSE` (if present).

[flexi]: https://github.com/moepig/flexi
