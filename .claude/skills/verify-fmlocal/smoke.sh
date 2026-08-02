#!/usr/bin/env bash
# smoke.sh — fmlocal FlexMatch イベント動作確認スクリプト
#
# 使い方:
#   bash .claude/skills/verify-fmlocal/smoke.sh <mode>
#
# モード:
#   completed            2プレイヤーがマッチング → MatchmakingSucceeded
#   cancelled            チケット即停止 → MatchmakingCancelled
#   timedout             requestTimeout 経過 → MatchmakingTimedOut  (約 65 秒)
#   accepted             承認フロー・全員 ACCEPT → AcceptMatchCompleted(Accepted) + MatchmakingSucceeded
#   rejected             承認フロー・1人 REJECT → AcceptMatchCompleted(Rejected) + 拒否=MatchmakingCancelled / 承認=再キュー(MatchmakingSearching)
#   acceptance-timedout  承認フロー・誰も応答しない → AcceptMatchCompleted(TimedOut) + 両チケット MatchmakingCancelled  (約 35 秒)
#   backfill             backfill でセッションの空席を埋める → MatchmakingSucceeded
#   backfill-superseded  同一 GameSessionArn への再リクエスト → 先行は MatchmakingCancelled
#   all                  全フロー順番に実行  (約 100 秒)
#
# 前提: docker compose up で fmlocal (9080) と ElasticMQ (9324) が起動済みであること

set -euo pipefail

API="http://localhost:9080"
SQS="http://localhost:9324"
QUEUE_URL="$SQS/000000000000/fmlocal-events"
G="aws gamelift --endpoint-url $API --region us-east-1 --no-sign-request --output json"
S="aws sqs     --endpoint-url $SQS --region us-east-1 --no-sign-request --output json"

MODE="${1:-}"
if [ -z "$MODE" ]; then
  echo "Usage: $0 <completed|cancelled|timedout|accepted|rejected|acceptance-timedout|backfill|backfill-superseded|all>"
  exit 1
fi

# ---------------------------------------------------------------------------
# ユーティリティ
# ---------------------------------------------------------------------------

purge() {
  $S purge-queue --queue-url "$QUEUE_URL" > /dev/null
  sleep 1
  echo "  [queue purged]"
}

# キューからメッセージを最大 $1 件受信して表示する。EMPTY になったら終了。
drain() {
  local max="${1:-20}"
  local vt="${2:-60}"
  for i in $(seq 1 "$max"); do
    local msg body typ
    msg=$($S receive-message --queue-url "$QUEUE_URL" \
      --max-number-of-messages 1 --visibility-timeout "$vt" 2>&1)
    body=$(echo "$msg" | python3 -c \
      "import sys,json; m=json.load(sys.stdin).get('Messages',[]); print(m[0]['Body'] if m else 'EMPTY')")
    [ "$body" = "EMPTY" ] && break
    typ=$(echo "$body" | python3 -c \
      "import sys,json; d=json.loads(sys.stdin.read()); print(d.get('detail',{}).get('type','?'))")
    echo "  [$i] $typ"
    echo "$body" | python3 -m json.tool
    echo ""
  done
}

# REQUIRES_ACCEPTANCE になるまでポーリング（最大 15 秒）
wait_acceptance() {
  local tid="$1"
  local elapsed=0
  while [ "$elapsed" -lt 15 ]; do
    local st
    st=$($G describe-matchmaking --ticket-ids "$tid" \
      | python3 -c "import sys,json; print(json.load(sys.stdin)['TicketList'][0]['Status'])")
    [ "$st" = "REQUIRES_ACCEPTANCE" ] && echo "  → REQUIRES_ACCEPTANCE" && return 0
    sleep 1
    elapsed=$((elapsed + 1))
  done
  echo "  ERROR: REQUIRES_ACCEPTANCE にならなかった" >&2
  exit 1
}

start_ticket() {
  local cfg="$1" player="$2" skill="$3"
  $G start-matchmaking \
    --configuration-name "$cfg" \
    --players "[{\"PlayerId\":\"$player\",\"PlayerAttributes\":{\"skill\":{\"N\":$skill}}}]" \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['MatchmakingTicket']['TicketId'])"
}

# 2v2 セッションの空席 1 つを埋める backfill リクエストを投げる。
# 座っている 3 人は Team 付きで報告する（AWS 同様、全員に Team が必須）。
start_backfill() {
  local tid="$1" gs="$2"
  $G start-match-backfill \
    --configuration-name backfill \
    --ticket-id "$tid" \
    --game-session-arn "arn:aws:gamelift:us-east-1:000000000000:gamesession/$gs" \
    --players '[
      {"PlayerId":"smoke-bf-r1","Team":"red","PlayerAttributes":{"skill":{"N":1500}}},
      {"PlayerId":"smoke-bf-r2","Team":"red","PlayerAttributes":{"skill":{"N":1510}}},
      {"PlayerId":"smoke-bf-b1","Team":"blue","PlayerAttributes":{"skill":{"N":1505}}}
    ]' \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['MatchmakingTicket']['TicketId'])"
}

# ---------------------------------------------------------------------------
# フロー
# ---------------------------------------------------------------------------

flow_completed() {
  echo ""
  echo "=== [completed] 2プレイヤーをマッチング → MatchmakingSucceeded ==="
  purge
  start_ticket default smoke-c1 1500 > /dev/null
  start_ticket default smoke-c2 1510 > /dev/null
  sleep 2
  drain 8
}

flow_cancelled() {
  echo ""
  echo "=== [cancelled] チケット投入 → 即停止 → MatchmakingCancelled ==="
  purge
  local tid
  tid=$(start_ticket default smoke-x1 9999)
  echo "  ticket: $tid"
  sleep 1
  $G stop-matchmaking --ticket-id "$tid" > /dev/null
  echo "  stopped"
  sleep 1
  drain 4
}

flow_timedout() {
  echo ""
  echo "=== [timedout] 対戦相手なしで requestTimeout 経過 → MatchmakingTimedOut (約 65 秒) ==="
  purge
  local tid
  tid=$(start_ticket default smoke-t1 99999)
  echo "  ticket: $tid"
  echo "  62 秒待機中 (requestTimeoutSeconds=60)..."
  sleep 62
  drain 4
}

flow_accepted() {
  echo ""
  echo "=== [accepted] 承認フロー・全員 ACCEPT → AcceptMatchCompleted(Accepted) + MatchmakingSucceeded ==="
  purge
  local t1 t2
  t1=$(start_ticket accept smoke-a1 2000)
  t2=$(start_ticket accept smoke-a2 2010)
  echo "  ticket1=$t1 ticket2=$t2"
  wait_acceptance "$t1"
  $G accept-match --ticket-id "$t1" --player-ids smoke-a1 --acceptance-type ACCEPT > /dev/null
  $G accept-match --ticket-id "$t2" --player-ids smoke-a2 --acceptance-type ACCEPT > /dev/null
  echo "  両者 ACCEPT 送信"
  sleep 2
  drain 12
}

flow_rejected() {
  echo ""
  echo "=== [rejected] 承認フロー・1人 REJECT → AcceptMatchCompleted(Rejected) + 拒否=MatchmakingCancelled / 承認=再キュー(MatchmakingSearching) ==="
  purge
  local t1 t2
  t1=$(start_ticket accept smoke-r1 3000)
  t2=$(start_ticket accept smoke-r2 3010)
  echo "  ticket1=$t1 (ACCEPT) ticket2=$t2 (REJECT)"
  wait_acceptance "$t1"
  $G accept-match --ticket-id "$t1" --player-ids smoke-r1 --acceptance-type ACCEPT > /dev/null
  $G accept-match --ticket-id "$t2" --player-ids smoke-r2 --acceptance-type REJECT > /dev/null
  echo "  smoke-r1=ACCEPT smoke-r2=REJECT 送信"
  sleep 2
  drain 12
  # 再キューされた t1 を後始末
  $G stop-matchmaking --ticket-id "$t1" > /dev/null 2>&1 || true
}

flow_backfill() {
  echo ""
  echo "=== [backfill] セッションの空席を backfill で埋める → MatchmakingSucceeded ==="
  purge
  local bf tid
  bf=$(start_backfill smoke-bf-1 gs-1)
  echo "  backfill ticket: $bf (red×2 + blue×1 着席、blue が 1 席空き)"
  tid=$(start_ticket backfill smoke-bf-join 1505)
  echo "  new ticket: $tid"
  sleep 2
  drain 8
}

flow_backfill_superseded() {
  echo ""
  echo "=== [backfill-superseded] 同一 GameSessionArn への再リクエスト → 先行は MatchmakingCancelled ==="
  purge
  start_backfill smoke-bf-old gs-2 > /dev/null
  echo "  1件目: smoke-bf-old"
  start_backfill smoke-bf-newer gs-2 > /dev/null
  echo "  2件目: smoke-bf-newer（同一セッション）"
  sleep 1
  drain 6
  # 置き換え後に残るチケットを後始末
  $G stop-matchmaking --ticket-id smoke-bf-newer > /dev/null 2>&1 || true
}

flow_acceptance_timedout() {
  echo ""
  echo "=== [acceptance-timedout] 誰も承認しない → AcceptMatchCompleted(TimedOut) + 両チケット MatchmakingCancelled (約 35 秒) ==="
  purge
  local t1 t2
  t1=$(start_ticket accept smoke-at1 4000)
  t2=$(start_ticket accept smoke-at2 4010)
  echo "  ticket1=$t1 ticket2=$t2"
  wait_acceptance "$t1"
  echo "  承認せず 32 秒待機 (acceptanceTimeoutSeconds=30)..."
  sleep 32
  drain 10
}

# ---------------------------------------------------------------------------
# メイン
# ---------------------------------------------------------------------------

echo "=== ヘルスチェック ==="
curl -sf "$API/healthz" && echo " OK" || { echo " FAILED"; exit 1; }

case "$MODE" in
  completed)           flow_completed ;;
  cancelled)           flow_cancelled ;;
  timedout)            flow_timedout  ;;
  accepted)            flow_accepted  ;;
  rejected)            flow_rejected  ;;
  acceptance-timedout) flow_acceptance_timedout ;;
  backfill)            flow_backfill ;;
  backfill-superseded) flow_backfill_superseded ;;
  all)
    flow_completed
    flow_cancelled
    flow_accepted
    flow_rejected
    flow_acceptance_timedout
    flow_backfill
    flow_backfill_superseded
    flow_timedout
    ;;
  *)
    echo "不明なモード: $MODE"
    echo "Usage: $0 <completed|cancelled|timedout|accepted|rejected|acceptance-timedout|backfill|backfill-superseded|all>"
    exit 1
    ;;
esac

echo ""
echo "=== 完了 ==="
