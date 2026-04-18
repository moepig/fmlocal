# Verifying fmlocal with the AWS CLI

fmlocal implements enough of the GameLift JSON 1.1 wire protocol that the standard AWS CLI works against it as long as you pass `--endpoint-url`. This document walks through a full matchmaking lifecycle using only `aws gamelift` commands.

## Prerequisites

- fmlocal running locally (see [Running locally](../usage/local.md)).
- `aws` CLI v2 installed.
- A stub set of credentials — fmlocal does not validate them, but the CLI refuses to run without them. The simplest option:

  ```sh
  export AWS_ACCESS_KEY_ID=x
  export AWS_SECRET_ACCESS_KEY=x
  export AWS_REGION=us-east-1
  ```

All examples below assume the endpoint and region are set via flags for clarity. You can also set `AWS_ENDPOINT_URL=http://localhost:9080` (CLI v2 ≥ 2.15) to drop the flag.

## Quick health check

```sh
curl -s http://localhost:9080/healthz
# => ok
```

```sh
aws gamelift describe-matchmaking-configurations \
  --endpoint-url http://localhost:9080 \
  --region us-east-1
```

You should see the configurations defined in your `config.yaml` (`default` and `accept` when using `deploy/local/config.yaml`).

## List rule sets

```sh
aws gamelift describe-matchmaking-rule-sets \
  --endpoint-url http://localhost:9080 \
  --region us-east-1
```

Each rule set's `RuleSetBody` is returned verbatim from the file configured under `ruleSets[*].path`.

## Validate a rule set

```sh
aws gamelift validate-matchmaking-rule-set \
  --endpoint-url http://localhost:9080 \
  --region us-east-1 \
  --rule-set-body "$(cat deploy/local/rulesets/1v1.json)"
```

Returns `{"Valid": true}` when the body parses; otherwise a `InvalidRequestException` with a parse error.

## Start a ticket

The `default` configuration uses the `1v1` rule set (two teams of one, matched on `skill`). Post two tickets that should match:

```sh
aws gamelift start-matchmaking \
  --endpoint-url http://localhost:9080 \
  --region us-east-1 \
  --configuration-name default \
  --ticket-id ticket-alice \
  --players '[
    {
      "PlayerId": "alice",
      "PlayerAttributes": { "skill": { "N": 1500 } }
    }
  ]'
```

```sh
aws gamelift start-matchmaking \
  --endpoint-url http://localhost:9080 \
  --region us-east-1 \
  --configuration-name default \
  --ticket-id ticket-bob \
  --players '[
    {
      "PlayerId": "bob",
      "PlayerAttributes": { "skill": { "N": 1520 } }
    }
  ]'
```

Each call returns a `MatchmakingTicket` in status `QUEUED`. The ticker advances the matchmaker every `tickInterval` (500ms in the sample config).

## Describe tickets

```sh
aws gamelift describe-matchmaking \
  --endpoint-url http://localhost:9080 \
  --region us-east-1 \
  --ticket-ids ticket-alice ticket-bob
```

Within a second or two, both tickets transition through `SEARCHING` → `PLACING` → `COMPLETED`. When `acceptanceRequired: true` (e.g. the `accept` configuration), tickets pause in `REQUIRES_ACCEPTANCE` until every player has accepted.

## Accept a proposal

Using the `accept` configuration (`1v1-accept` rule set):

```sh
aws gamelift start-matchmaking \
  --endpoint-url http://localhost:9080 \
  --region us-east-1 \
  --configuration-name accept \
  --ticket-id accept-alice \
  --players '[{"PlayerId":"alice","PlayerAttributes":{"skill":{"N":1500}}}]'

aws gamelift start-matchmaking \
  --endpoint-url http://localhost:9080 \
  --region us-east-1 \
  --configuration-name accept \
  --ticket-id accept-bob \
  --players '[{"PlayerId":"bob","PlayerAttributes":{"skill":{"N":1520}}}]'
```

Once both tickets move to `REQUIRES_ACCEPTANCE`:

```sh
aws gamelift accept-match \
  --endpoint-url http://localhost:9080 \
  --region us-east-1 \
  --ticket-id accept-alice \
  --player-ids alice \
  --acceptance-type ACCEPT

aws gamelift accept-match \
  --endpoint-url http://localhost:9080 \
  --region us-east-1 \
  --ticket-id accept-bob \
  --player-ids bob \
  --acceptance-type ACCEPT
```

After both accepts, the tickets advance to `PLACING` and then `COMPLETED`. If any player sends `--acceptance-type REJECT`, all tickets in that proposal are moved to the `CANCELLED`/`FAILED` terminal states and a `MatchmakingFailed` event is emitted.

## Stop a ticket

```sh
aws gamelift stop-matchmaking \
  --endpoint-url http://localhost:9080 \
  --region us-east-1 \
  --ticket-id ticket-alice
```

The ticket transitions to `CANCELLED` on the next tick; an empty JSON body is returned on success.

## Inspecting events

Every lifecycle transition is dispatched to whatever publisher the ticket's matchmaking configuration is bound to. For how to read them (SQS via the AWS CLI, or an HTTP listener for the SNS kind), the full event catalog, and the envelope shape, see [Event publishers](../feature/publishers.md).

## Error responses

fmlocal maps domain errors to the GameLift-documented error codes:

| Scenario                                  | HTTP | Error code                 |
|-------------------------------------------|------|----------------------------|
| Unknown `X-Amz-Target`                    | 400  | `UnknownOperationException`|
| Missing / malformed input                 | 400  | `InvalidRequestException`  |
| `NotFound` on ticket/config/rule set      | 404  | `NotFoundException`        |
| Invalid state transition                  | 400  | `InvalidRequestException`  |
| Anything else                             | 500  | `InternalServiceException` |

## Unsupported operations

`StartMatchBackfill` and `StopMatchBackfill` intentionally return `UnsupportedOperation` — fmlocal does not implement game-session placement.
