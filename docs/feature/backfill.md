# Match backfill

Backfill is a request to fill the empty seats of a game session that is already under way, rather than to form a new match. A game server that has lost players calls `StartMatchBackfill` with everyone still in the session, each tagged with the team they occupy; fmlocal seats those players and matches new tickets around them, so the session keeps satisfying the rule set it was formed under.

fmlocal implements the manual backfill of FlexMatch's standalone mode. Automatic backfill is not available: AWS itself offers it only when FlexMatch places the game session, which `STANDALONE` does not.

## Making a request

`StartMatchBackfill` takes the following members.

| Member | Required | Description |
|---|---|---|
| `ConfigurationName` | yes | The matchmaking configuration the request is queued against. |
| `Players` | yes | Everyone currently in the game session, at most 199. Every player must carry a `Team`. |
| `TicketId` | no | Identifier for the resulting ticket. Generated when omitted. |
| `GameSessionArn` | no | The session being refilled. See [One request per game session](#one-request-per-game-session). |

`Team` names a team the rule set declares. A team declared with `quantity` greater than 1 must be given as the expanded name the player was reported under, such as `red_2`, because the base name alone does not say which instance the player sits on. A roster fmlocal or the engine rejects — a missing, unknown, or ambiguous team, a team over its `maxPlayers`, an attribute whose type disagrees with the rule set — answers with `InvalidRequestException`.

`GameSessionArn` is not validated as an ARN and is not resolved to anything: fmlocal knows nothing about game sessions, and AWS documents the member as unnecessary in standalone mode. It serves only as the key one request supersedes another by.

`Team` is rejected on `StartMatchmaking`, mirroring AWS's "do not specify a team if you are not using backfill". Earlier versions of fmlocal silently ignored it.

## Ticket lifecycle

A backfill ticket is an ordinary matchmaking ticket in every respect the API exposes. It is reported by `DescribeMatchmaking`, stopped with `StopMatchmaking` — there is no `StopMatchBackfill` operation in the GameLift API — subject to the configuration's `requestTimeoutSeconds`, and proposed for acceptance under `acceptanceRequired` like any other ticket. When acceptance is required, the players already in the session must accept too; the game server calls `AcceptMatch` on their behalf, as it would against AWS.

The events are the ordinary ones as well, with no new type and no new field. The one difference is that a backfill ticket reports its players' teams from the moment the request is made, where a regular ticket has none until a match forms. Once a match forms the engine's assignment replaces it, and `MatchmakingSucceeded` reports every seat in the session — the players already there alongside the ones joining.

`gameSessionInfo.gameSessionArn` stays absent even when the request named one, because standalone mode does not place game sessions and AWS omits the field too.

## One request per game session

Setting `GameSessionArn` opts into FlexMatch's rule that a game session has at most one outstanding backfill request. A new request for the same session supersedes the one still waiting, which ends `CANCELLED` with the status message `Superseded by a newer backfill request for the same game session` and emits `MatchmakingCancelled`. The wire event is the shape AWS sends for any cancellation, so a consumer that needs to tell the causes apart reads the status message through `DescribeMatchmaking`.

Leaving `GameSessionArn` empty skips this bookkeeping entirely, and the requests simply queue alongside each other.

## Reaching for backfill requests

`algorithm.backfillPriority` in the rule set decides when matchmaking reaches for a queued backfill request.

| Value | Behaviour |
|---|---|
| `high` | Try every backfill request, oldest first, before forming a new match. |
| `low` | Reach for a backfill request only once no new match can be formed. |
| `normal` | No special standing: a request takes part when it is the oldest ticket queued. |

The default is `normal`. As in FlexMatch the property applies to the `exhaustiveSearch` strategy only; a rule set may declare it alongside `balanced`, where it has no effect.

At most one backfill request takes part in any match, and a match only forms if at least one new ticket joins it.

## Differences from AWS

| Item | AWS | fmlocal |
|---|---|---|
| Superseding a request that is already matched | Assumed to replace it | `InvalidRequestException`. Tearing down a live proposal would cancel the sibling tickets in it and emit an acceptance outcome nobody asked for. |
| One request per session without `GameSessionArn` | Enforced through the game session | Not enforced; the requests coexist. The member is optional in standalone mode. |
| `GameSessionArn` format and existence | An ARN of a live session | Not validated. |
