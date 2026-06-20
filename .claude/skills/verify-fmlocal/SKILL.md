---
name: verify-fmlocal
description: Verify, smoke test, or check FlexMatch matchmaking event flows for fmlocal. Confirms MatchmakingSearching, MatchmakingSucceeded, MatchmakingCancelled, MatchmakingTimedOut, PotentialMatchCreated, AcceptMatch, and AcceptMatchCompleted events — including the acceptance-failure re-queue (MatchmakingSearching) and per-ticket cancellation (MatchmakingCancelled) — appear in ElasticMQ after docker compose up.
---

# verify-fmlocal

fmlocal の FlexMatch イベントフロー検証スキル。`docker compose up` で起動後に `.claude/skills/verify-fmlocal/smoke.sh` を実行して、各ライフサイクルで正しいイベントが ElasticMQ の SQS キューに投入されることを確認する。

## 前提

- Docker Engine + Compose v2
- `aws` CLI v2
- `python3`（JSON 整形に使用）

AWS 認証情報は不要。CLI が起動するだけのダミー値で十分:

```sh
export AWS_ACCESS_KEY_ID=x AWS_SECRET_ACCESS_KEY=x
```

## 起動

```sh
docker compose up --build
```

```sh
curl -s http://localhost:9080/healthz
# => ok
```

## ドライバー（エージェントパス）

```sh
bash .claude/skills/verify-fmlocal/smoke.sh <mode>
```

| モード | 内容 | 所要時間 |
|---|---|---|
| `completed` | 2プレイヤーがマッチング | 数秒 |
| `cancelled` | チケット投入 → 即停止 | 数秒 |
| `accepted` | 承認フロー・全員 ACCEPT | 数秒 |
| `rejected` | 承認フロー・1人 REJECT | 数秒 |
| `acceptance-timedout` | 承認フロー・誰も応答しない | 約 35 秒 |
| `timedout` | requestTimeout 経過 | 約 65 秒 |
| `all` | 全フロー順番に実行 | 約 100 秒 |

各モードはキューをパージしてから実行するため、前回の残留メッセージの影響を受けない。

## イベントカタログ

全イベントは EventBridge エンベロープ形式（`detail-type: "GameLift Matchmaking Event"`）。

`MatchmakingSearching` と `AcceptMatch` は**チケット単位**で発行する。それ以外（`PotentialMatchCreated` / `AcceptMatchCompleted` / `MatchmakingSucceeded`）は**マッチ単位で1回**だけ発行し、`detail.tickets` に全チケットを格納する。`MatchmakingCancelled` / `MatchmakingTimedOut` は終了したチケットごとに発行する。

| `detail.type` | 発生タイミング | 設定 | `detail.reason` |
|---|---|---|---|
| `MatchmakingSearching` | チケット投入直後／承認失敗で再キューされた時 | 全て | — |
| `MatchmakingSucceeded` | マッチ成立 | 全て | — |
| `MatchmakingCancelled` | `stop-matchmaking` 呼び出し／承認失敗で打ち切られたチケット | 全て | `"Cancelled"` |
| `MatchmakingTimedOut` | `requestTimeoutSeconds` 経過（リクエストレベルのみ）| 全て | `"TimedOut"` |
| `PotentialMatchCreated` | マッチ候補確定（直接マッチ・承認フロー両方）| 全て | — |
| `AcceptMatch` | 各プレイヤーの承認・拒否記録 | `accept` のみ | — |
| `AcceptMatchCompleted` | 承認フェーズ完了 | `accept` のみ | `Accepted` / `Rejected` / `TimedOut` |

> `MatchmakingFailed` はランタイムでは発行しない。AWS はキュー配置／内部エラー専用に予約しており、承認失敗（reject / 承認タイムアウト）には使わない。

## フロー別イベントシーケンス

```
[completed]
  MatchmakingSearching   チケットごと（×2）
  PotentialMatchCreated  ×1  → 直接（承認なし）マッチでも発行
  MatchmakingSucceeded   ×1

[cancelled]
  MatchmakingSearching
  MatchmakingCancelled  detail.message: "Matchmaking stopped by client"

[timedout]
  MatchmakingSearching
  MatchmakingTimedOut   detail.message: "Matchmaking request timed out"

[accepted]  ← accept 設定
  MatchmakingSearching   チケットごと（×2）
  PotentialMatchCreated  ×1  → detail.matchId が付く
  AcceptMatch            チケットごと（×2）→ players[].accepted: true
  AcceptMatchCompleted   ×1  → acceptance: "Accepted"
  MatchmakingSucceeded   ×1

[rejected]  ← accept 設定
  MatchmakingSearching   ×2（投入時）
  PotentialMatchCreated  ×1
  AcceptMatch            ×2  → 承認者: accepted=true / 拒否者: accepted=false
  AcceptMatchCompleted   ×1  → acceptance: "Rejected"
  MatchmakingCancelled   ×1  → 拒否したチケットのみ（MatchmakingFailed ではない）
  MatchmakingSearching   ×1  → 承認したチケットはプールへ再キュー
                               （DescribeMatchmaking の statusReason=ACCEPTANCE_FAILED）

[acceptance-timedout]  ← accept 設定
  MatchmakingSearching   ×2
  PotentialMatchCreated  ×1
  AcceptMatchCompleted   ×1  → acceptance: "TimedOut"
  MatchmakingCancelled   ×2  → 誰も承認しなかったので両チケットとも打ち切り
                               （MatchmakingTimedOut ではない）
```

## 設定ファイル

| ファイル | 役割 |
|---|---|
| `deploy/local/config.yaml` | `default`（承認なし, timeout=60s）/ `accept`（承認あり, acceptTimeout=30s, timeout=120s） |
| `deploy/local/rulesets/1v1.json` | skill 差 50 以内でマッチ |
| `deploy/local/elasticmq.conf` | `fmlocal-events` キュー定義 |

## ゴッチャ

- **承認失敗に `MatchmakingFailed` は使わない**: reject / 承認タイムアウトでは、承認が揃ったチケットはプールへ戻され `MatchmakingSearching` が再発行される（`DescribeMatchmaking` の `statusReason=ACCEPTANCE_FAILED`）。失敗を招いたチケットだけが `MatchmakingCancelled` で終わる。AWS は `MatchmakingFailed` をキュー配置／内部エラー専用に予約しているため、ランタイムでは発行されない。
- **`MatchmakingTimedOut` はリクエストレベル専用**: `requestTimeoutSeconds` 経過時のみ発行する。承認タイムアウト（`acceptanceTimeoutSeconds` 経過）は `MatchmakingTimedOut` ではなく `MatchmakingCancelled`（＋ `AcceptMatchCompleted` acceptance=`TimedOut`）になる。
- **`rejected` 後は承認チケットが残る**: 再キューされたチケットは `SEARCHING` のまま生き続けるため、smoke.sh は後始末で `stop-matchmaking` を呼ぶ。
- **SQS visibility timeout のデフォルトは 10 秒**: `receive-message` で取得したメッセージを `--visibility-timeout 60` なしで読むと、10 秒後にキューへ戻って重複受信する。smoke.sh は 60 秒を指定している。
- **`--no-sign-request` が必要**: ダミー認証情報（`AWS_ACCESS_KEY_ID=x`）でも CLI が起動するが、`--no-sign-request` がないと署名エラーになる。

## 停止

```sh
docker compose down
```
