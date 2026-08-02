package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	"github.com/aws/aws-sdk-go-v2/service/gamelift/types"

	"github.com/moepig/flexi"
	"github.com/moepig/fmlocal/internal/app/defaults/idgen"
	"github.com/moepig/fmlocal/internal/app/defaults/sysclock"
	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	"github.com/moepig/fmlocal/internal/app/ports"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"github.com/moepig/fmlocal/internal/infrastructure/notification"
	"github.com/moepig/fmlocal/internal/interfaces/awsapi"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const acceptanceRuleSet = `{
  "name": "1v1-accept",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [{"name": "skill", "type": "number"}],
  "teams": [
    {"name": "red",  "minPlayers": 1, "maxPlayers": 1},
    {"name": "blue", "minPlayers": 1, "maxPlayers": 1}
  ],
  "rules": [
    {
      "name": "FairSkill",
      "type": "distance",
      "measurements": ["avg(teams[red].players.attributes[skill])"],
      "referenceValue": "avg(teams[blue].players.attributes[skill])",
      "maxDistance": 50
    }
  ],
  "acceptanceRequired": true,
  "acceptanceTimeoutSeconds": 30
}`

const basicRuleSet = `{
  "name": "1v1",
  "ruleLanguageVersion": "1.0",
  "teams": [
    {"name": "red",  "minPlayers": 1, "maxPlayers": 1},
    {"name": "blue", "minPlayers": 1, "maxPlayers": 1}
  ]
}`

type eventSink struct {
	mu      sync.Mutex
	events  []notification.EventBridgeEnvelope
	rawMsgs []json.RawMessage // parallel to events: the exact envelope JSON received
	srv     *httptest.Server
}

func newEventSink(t *testing.T) *eventSink {
	t.Helper()
	s := &eventSink{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var note notification.SNSNotification
		if err := json.Unmarshal(body, &note); err != nil {
			t.Errorf("sink: decode notification: %v", err)
			http.Error(w, err.Error(), 400)
			return
		}
		var env notification.EventBridgeEnvelope
		if err := json.Unmarshal([]byte(note.Message), &env); err != nil {
			t.Errorf("sink: decode envelope: %v", err)
			http.Error(w, err.Error(), 400)
			return
		}
		s.mu.Lock()
		s.events = append(s.events, env)
		s.rawMsgs = append(s.rawMsgs, json.RawMessage(note.Message))
		s.mu.Unlock()
		w.WriteHeader(200)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *eventSink) detailTypes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.events))
	for _, e := range s.events {
		out = append(out, e.Detail.Type)
	}
	return out
}

// ticketIDsOf collects the ticketIds present in an envelope's detail.tickets.
func ticketIDsOf(env notification.EventBridgeEnvelope) []string {
	out := make([]string, 0, len(env.Detail.Tickets))
	for _, tk := range env.Detail.Tickets {
		out = append(out, tk.TicketID)
	}
	return out
}

// rawEnvelopesOfType returns the received envelopes of a given detail.type as
// generic maps, so a test can assert on the exact JSON keys actually emitted
// (catching both missing and unexpected fields, which the typed struct hides).
func (s *eventSink) rawEnvelopesOfType(typ string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for i, e := range s.events {
		if e.Detail.Type != typ {
			continue
		}
		var env map[string]any
		if err := json.Unmarshal(s.rawMsgs[i], &env); err == nil {
			out = append(out, env)
		}
	}
	return out
}

func keySet(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func asMaps(v any) []map[string]any {
	arr, _ := v.([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		out = append(out, e.(map[string]any))
	}
	return out
}

// eventShape declares the exact element set an event's JSON must contain, used
// to verify happy-path messages exhaustively.
type eventShape struct {
	detailKeys     []string // exact set of detail.* keys
	playerRequired []string // keys every player object must have
	playerOptional []string // keys a player object may additionally have
	gsiKeys        []string // exact set of gameSessionInfo.* keys
}

// assertEnvelopeShape verifies the EventBridge envelope wrapper is complete.
func assertEnvelopeShape(t *testing.T, env map[string]any) {
	t.Helper()
	assert.ElementsMatch(t, []string{
		"version", "id", "detail-type", "source", "account", "time", "region", "resources", "detail",
	}, keySet(env), "envelope keys")
	assert.Equal(t, "aws.gamelift", env["source"])
	assert.Equal(t, "GameLift Matchmaking Event", env["detail-type"])
	assert.Equal(t, "us-east-1", env["region"])
	assert.Equal(t, "000000000000", env["account"])
	// AWS uses ISO-8601 with millisecond precision.
	assert.Regexp(t, `^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`, env["time"])
	res := env["resources"].([]any)
	require.Len(t, res, 1)
	assert.Equal(t, "arn:aws:gamelift:us-east-1:000000000000:matchmakingconfiguration/cfg", res[0])
}

// assertDetailShape verifies a detail block matches the declared shape exactly:
// the detail key set, ticket/player key sets, and gameSessionInfo key set.
func assertDetailShape(t *testing.T, detail map[string]any, s eventShape) {
	t.Helper()
	assert.ElementsMatch(t, s.detailKeys, keySet(detail), "detail keys")

	tickets := asMaps(detail["tickets"])
	require.NotEmpty(t, tickets, "tickets present")
	for _, tk := range tickets {
		assert.ElementsMatch(t, []string{"ticketId", "startTime", "players"}, keySet(tk), "ticket keys")
		assert.Regexp(t, `T\d\d:\d\d:\d\d\.\d{3}Z$`, tk["startTime"], "ticket startTime millis")
		require.NotEmpty(t, asMaps(tk["players"]), "ticket players present")
		for _, p := range asMaps(tk["players"]) {
			assertPlayerShape(t, p, s)
		}
	}

	gsi, ok := detail["gameSessionInfo"].(map[string]any)
	require.True(t, ok, "gameSessionInfo present")
	assert.ElementsMatch(t, s.gsiKeys, keySet(gsi), "gameSessionInfo keys")
	gsiPlayers := asMaps(gsi["players"])
	require.NotEmpty(t, gsiPlayers, "gameSessionInfo players present")
	for _, p := range gsiPlayers {
		assertPlayerShape(t, p, s)
	}

	// gameSessionInfo.players must be exactly the roster flattened across the
	// event's tickets — same playerId/team/accepted values, not just key shape.
	var flatTicketPlayers []map[string]any
	for _, tk := range tickets {
		flatTicketPlayers = append(flatTicketPlayers, asMaps(tk["players"])...)
	}
	assert.ElementsMatch(t, flatTicketPlayers, gsiPlayers, "gameSessionInfo players mirror tickets")
}

func assertPlayerShape(t *testing.T, p map[string]any, s eventShape) {
	t.Helper()
	for _, k := range s.playerRequired {
		assert.Containsf(t, p, k, "player missing required key %q", k)
	}
	allowed := append(append([]string{}, s.playerRequired...), s.playerOptional...)
	for k := range p {
		assert.Containsf(t, allowed, k, "player has unexpected key %q (player=%v)", k, p)
	}
}

func (s *eventSink) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, typ := range s.detailTypes() {
			if typ == want {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for event %q; got %v", want, s.detailTypes())
}

type stack struct {
	httpSrv *httptest.Server
	sink    *eventSink
	svc     *appmm.Service
}

func buildStack(t *testing.T, acceptance bool) *stack {
	t.Helper()
	if acceptance {
		return buildStackWith(t, "1v1-accept", acceptanceRuleSet, true)
	}
	return buildStackWith(t, "1v1", basicRuleSet, false)
}

// buildStackWith runs the whole server stack — engine, ticker, notification
// publisher and AWS API — over the given rule set, so a test can exercise a
// shape the two default 1v1 sets do not cover.
func buildStackWith(t *testing.T, rsName mm.RuleSetName, ruleSet string, acceptance bool) *stack {
	t.Helper()
	sink := newEventSink(t)
	rsBody := []byte(ruleSet)

	clk := sysclock.System{}
	ids := idgen.NewUUID()
	cfg := mm.Configuration{
		Name:               "cfg",
		ARN:                "arn:aws:gamelift:us-east-1:000000000000:matchmakingconfiguration/cfg",
		RuleSetName:        rsName,
		RuleSetARN:         "arn:aws:gamelift:us-east-1:000000000000:matchmakingruleset/" + string(rsName),
		FlexMatchMode:      mm.FlexMatchModeStandalone,
		RequestTimeout:     60 * time.Second,
		AcceptanceRequired: acceptance,
		AcceptanceTimeout:  30 * time.Second,
		NotificationTargetIDs: []string{"sink"},
	}
	rs := mm.RuleSet{Name: rsName, ARN: cfg.RuleSetARN, Body: rsBody}

	engine, err := appmm.BuildEngine(cfg, rs, flexi.WithClock(clk))
	require.NoError(t, err)
	resolver := appmm.NewStaticEngineResolver()
	resolver.Register(cfg.Name, engine)

	svc := &appmm.Service{
		Engines:  resolver,
		Clock:    clk,
		IDs:      ids,
		MatchIDs: idgen.NewUUID(),
	}
	svc.LoadConfigurations([]mm.Configuration{cfg})
	svc.LoadRuleSets([]mm.RuleSet{rs})

	translator := notification.NewTranslator(ids,
		notification.EnvelopeSettings{Region: "us-east-1", AccountID: "000000000000"},
		func(id mm.TicketID) (notification.TicketDetail, bool) {
			tk, err := svc.GetTicket(id)
			if err != nil {
				return notification.TicketDetail{}, false
			}
			players := make([]notification.PlayerDetail, 0, len(tk.Players()))
			for _, p := range tk.Players() {
				players = append(players, notification.PlayerDetail{
					PlayerID: string(p.ID),
					Team:     tk.PlayerTeam(mm.PlayerID(p.ID)),
				})
			}
			return notification.TicketDetail{
				TicketID:  string(tk.ID()),
				StartTime: tk.StartTime().UTC().Format(notification.ISO8601Millis),
				Players:   players,
			}, true
		},
	)
	publisher := notification.NewSNSHTTPPublisher(sink.srv.URL, translator, ids, http.DefaultClient)
	svc.Publishers = map[mm.ConfigurationName]ports.EventPublisher{cfg.Name: publisher}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go (&appmm.Ticker{Service: svc, Names: []mm.ConfigurationName{cfg.Name}}).Run(ctx, 50*time.Millisecond)

	apiSrv := awsapi.NewServer(svc, awsapi.Options{}, nil)
	httpSrv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(httpSrv.Close)

	return &stack{httpSrv: httpSrv, sink: sink, svc: svc}
}

func newGameLiftClient(t *testing.T, endpoint string) *gamelift.Client {
	t.Helper()
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("x", "x", "")),
	)
	require.NoError(t, err)
	return gamelift.NewFromConfig(awsCfg, func(o *gamelift.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	})
}

func waitForTicketStatus(t *testing.T, client *gamelift.Client, ticketID string, want ...string) *types.MatchmakingTicket {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := client.DescribeMatchmaking(context.Background(), &gamelift.DescribeMatchmakingInput{TicketIds: []string{ticketID}})
		require.NoError(t, err)
		require.Len(t, out.TicketList, 1)
		tk := &out.TicketList[0]
		for _, w := range want {
			if string(tk.Status) == w {
				return tk
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for ticket %s to reach %v", ticketID, want)
	return nil
}

func TestE2E_StandaloneMatch_NoAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, false)
	client := newGameLiftClient(t, st.httpSrv.URL)

	for _, id := range []string{"t1", "t2"} {
		_, err := client.StartMatchmaking(context.Background(), &gamelift.StartMatchmakingInput{
			ConfigurationName: aws.String("cfg"),
			TicketId:          aws.String(id),
			Players: []types.Player{{
				PlayerId:         aws.String("p-" + id),
				PlayerAttributes: map[string]types.AttributeValue{"skill": {N: aws.Float64(50)}},
			}},
		})
		require.NoError(t, err, "start matchmaking for %s", id)
	}
	for _, id := range []string{"t1", "t2"} {
		tk := waitForTicketStatus(t, client, id, "COMPLETED")
		assert.Equal(t, "COMPLETED", string(tk.Status), id)
	}
	st.sink.waitFor(t, "MatchmakingSucceeded")

	// The no-acceptance happy path emits exactly these three event types.
	// Searching fires once per ticket; the rest once for the whole match.
	searching := st.sink.rawEnvelopesOfType("MatchmakingSearching")
	require.Len(t, searching, 2)
	pmcs := st.sink.rawEnvelopesOfType("PotentialMatchCreated")
	require.Len(t, pmcs, 1)
	succeededs := st.sink.rawEnvelopesOfType("MatchmakingSucceeded")
	require.Len(t, succeededs, 1)

	// --- MatchmakingSearching: single ticket, no team yet, NOT_AVAILABLE wait.
	searchShape := eventShape{
		detailKeys:     []string{"type", "tickets", "estimatedWaitMillis", "gameSessionInfo"},
		playerRequired: []string{"playerId"},
		gsiKeys:        []string{"players"},
	}
	for _, env := range searching {
		assertEnvelopeShape(t, env)
		d := env["detail"].(map[string]any)
		assertDetailShape(t, d, searchShape)
		assert.Equal(t, "NOT_AVAILABLE", d["estimatedWaitMillis"])
		require.Len(t, asMaps(d["tickets"]), 1) // one ticket per searching event
	}

	// --- PotentialMatchCreated: acceptanceRequired=false, no acceptanceTimeout,
	// no ruleEvaluationMetrics (basic rule set has no rules), teams assigned.
	pmcShape := eventShape{
		detailKeys:     []string{"type", "matchId", "tickets", "acceptanceRequired", "gameSessionInfo"},
		playerRequired: []string{"playerId", "team"},
		gsiKeys:        []string{"players"},
	}
	pmc := pmcs[0]
	assertEnvelopeShape(t, pmc)
	pd := pmc["detail"].(map[string]any)
	assertDetailShape(t, pd, pmcShape)
	assert.Equal(t, false, pd["acceptanceRequired"])
	assert.ElementsMatch(t, []string{"t1", "t2"}, rawTicketIDs(pd))
	assert.ElementsMatch(t, []string{"red", "blue"}, rawPlayerTeams(pd))

	// --- MatchmakingSucceeded: both tickets, teams, gameSessionInfo carries the
	// flattened roster + matchId; STANDALONE omits all connection fields and
	// playerSessionId.
	succShape := eventShape{
		detailKeys:     []string{"type", "matchId", "tickets", "gameSessionInfo"},
		playerRequired: []string{"playerId", "team"},
		gsiKeys:        []string{"players", "matchId"},
	}
	succ := succeededs[0]
	assertEnvelopeShape(t, succ)
	sd := succ["detail"].(map[string]any)
	assertDetailShape(t, sd, succShape)
	assert.NotEmpty(t, sd["matchId"])
	assert.ElementsMatch(t, []string{"t1", "t2"}, rawTicketIDs(sd))
	assert.ElementsMatch(t, []string{"red", "blue"}, rawPlayerTeams(sd))
	gsi := sd["gameSessionInfo"].(map[string]any)
	assert.Equal(t, sd["matchId"], gsi["matchId"])
}

// rawTicketIDs / rawPlayerTeams pull values out of a raw detail map for value
// assertions alongside the structural shape checks.
func rawTicketIDs(detail map[string]any) []string {
	out := []string{}
	for _, tk := range asMaps(detail["tickets"]) {
		out = append(out, tk["ticketId"].(string))
	}
	return out
}

func rawPlayerTeams(detail map[string]any) []string {
	out := []string{}
	for _, tk := range asMaps(detail["tickets"]) {
		for _, p := range asMaps(tk["players"]) {
			out = append(out, p["team"].(string))
		}
	}
	return out
}

func TestE2E_AcceptanceFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, true)
	client := newGameLiftClient(t, st.httpSrv.URL)

	for _, id := range []string{"t1", "t2"} {
		_, err := client.StartMatchmaking(context.Background(), &gamelift.StartMatchmakingInput{
			ConfigurationName: aws.String("cfg"),
			TicketId:          aws.String(id),
			Players: []types.Player{{
				PlayerId:         aws.String("p-" + id),
				PlayerAttributes: map[string]types.AttributeValue{"skill": {N: aws.Float64(50)}},
			}},
		})
		require.NoError(t, err)
	}
	for _, id := range []string{"t1", "t2"} {
		waitForTicketStatus(t, client, id, "REQUIRES_ACCEPTANCE")
		_, err := client.AcceptMatch(context.Background(), &gamelift.AcceptMatchInput{
			TicketId:       aws.String(id),
			PlayerIds:      []string{"p-" + id},
			AcceptanceType: types.AcceptanceTypeAccept,
		})
		require.NoError(t, err)
	}
	for _, id := range []string{"t1", "t2"} {
		waitForTicketStatus(t, client, id, "COMPLETED")
	}
	st.sink.waitFor(t, "MatchmakingSucceeded")

	// The acceptance happy path emits all five event types. Searching fires once
	// per ticket and AcceptMatch once per player accept; the grouping events
	// (PotentialMatchCreated, AcceptMatchCompleted, MatchmakingSucceeded) fire
	// once for the whole match.
	searching := st.sink.rawEnvelopesOfType("MatchmakingSearching")
	require.Len(t, searching, 2)
	pmcs := st.sink.rawEnvelopesOfType("PotentialMatchCreated")
	require.Len(t, pmcs, 1)
	accepts := st.sink.rawEnvelopesOfType("AcceptMatch")
	require.Len(t, accepts, 2)
	completeds := st.sink.rawEnvelopesOfType("AcceptMatchCompleted")
	require.Len(t, completeds, 1)
	succeededs := st.sink.rawEnvelopesOfType("MatchmakingSucceeded")
	require.Len(t, succeededs, 1)

	// --- MatchmakingSearching: single ticket, no team yet, NOT_AVAILABLE wait.
	searchShape := eventShape{
		detailKeys:     []string{"type", "tickets", "estimatedWaitMillis", "gameSessionInfo"},
		playerRequired: []string{"playerId"},
		gsiKeys:        []string{"players"},
	}
	for _, env := range searching {
		assertEnvelopeShape(t, env)
		d := env["detail"].(map[string]any)
		assertDetailShape(t, d, searchShape)
		assert.Equal(t, "NOT_AVAILABLE", d["estimatedWaitMillis"])
		require.Len(t, asMaps(d["tickets"]), 1)
	}

	// --- PotentialMatchCreated: acceptance policy + rule metrics + teams.
	pmcShape := eventShape{
		detailKeys:     []string{"type", "matchId", "tickets", "acceptanceRequired", "acceptanceTimeout", "ruleEvaluationMetrics", "gameSessionInfo"},
		playerRequired: []string{"playerId", "team"},
		gsiKeys:        []string{"players"},
	}
	pmc := pmcs[0]
	assertEnvelopeShape(t, pmc)
	pd := pmc["detail"].(map[string]any)
	assertDetailShape(t, pd, pmcShape)
	assert.Equal(t, true, pd["acceptanceRequired"])
	assert.Equal(t, float64(30), pd["acceptanceTimeout"]) // cfg AcceptanceTimeout 30s, emitted in seconds
	assert.ElementsMatch(t, []string{"t1", "t2"}, rawTicketIDs(pd))
	assert.ElementsMatch(t, []string{"red", "blue"}, rawPlayerTeams(pd))
	metrics := asMaps(pd["ruleEvaluationMetrics"])
	require.NotEmpty(t, metrics)
	var sawFair bool
	for _, m := range metrics {
		assert.ElementsMatch(t, []string{"ruleName", "passedCount", "failedCount"}, keySet(m), "metric keys")
		if m["ruleName"] == "FairSkill" {
			sawFair = true
			assert.GreaterOrEqual(t, m["passedCount"].(float64), float64(1))
		}
	}
	assert.True(t, sawFair, "FairSkill metric present")

	// --- AcceptMatch: per-accept event, both tickets, accepted on the actor.
	acceptShape := eventShape{
		detailKeys:     []string{"type", "matchId", "tickets", "gameSessionInfo"},
		playerRequired: []string{"playerId", "team"},
		playerOptional: []string{"accepted"},
		gsiKeys:        []string{"players"},
	}
	acceptedTrue := map[string]bool{}
	maxAcceptedInOneEvent := 0
	for _, env := range accepts {
		assertEnvelopeShape(t, env)
		d := env["detail"].(map[string]any)
		assertDetailShape(t, d, acceptShape)
		assert.ElementsMatch(t, []string{"t1", "t2"}, rawTicketIDs(d))
		acceptedHere := 0
		for _, tk := range asMaps(d["tickets"]) {
			for _, p := range asMaps(tk["players"]) {
				if acc, ok := p["accepted"]; ok && acc == true {
					acceptedTrue[p["playerId"].(string)] = true
					acceptedHere++
				}
			}
		}
		if acceptedHere > maxAcceptedInOneEvent {
			maxAcceptedInOneEvent = acceptedHere
		}
	}
	// Across the two AcceptMatch events both players are recorded as accepted.
	assert.Equal(t, map[string]bool{"p-t1": true, "p-t2": true}, acceptedTrue)
	// AcceptMatch reports cumulative state: the later event (after both accept)
	// shows BOTH players accepted in a single event, not just the latest actor.
	assert.Equal(t, 2, maxAcceptedInOneEvent, "a single AcceptMatch must reflect cumulative acceptance")

	// --- AcceptMatchCompleted: settled as Accepted, both tickets, teams.
	completedShape := eventShape{
		detailKeys:     []string{"type", "matchId", "tickets", "acceptance", "gameSessionInfo"},
		playerRequired: []string{"playerId", "team"},
		gsiKeys:        []string{"players"},
	}
	comp := completeds[0]
	assertEnvelopeShape(t, comp)
	cd := comp["detail"].(map[string]any)
	assertDetailShape(t, cd, completedShape)
	assert.Equal(t, "Accepted", cd["acceptance"])
	assert.ElementsMatch(t, []string{"t1", "t2"}, rawTicketIDs(cd))

	// --- MatchmakingSucceeded: both tickets, teams, gameSessionInfo + matchId.
	succShape := eventShape{
		detailKeys:     []string{"type", "matchId", "tickets", "gameSessionInfo"},
		playerRequired: []string{"playerId", "team"},
		gsiKeys:        []string{"players", "matchId"},
	}
	succ := succeededs[0]
	assertEnvelopeShape(t, succ)
	sd := succ["detail"].(map[string]any)
	assertDetailShape(t, sd, succShape)
	assert.NotEmpty(t, sd["matchId"])
	assert.ElementsMatch(t, []string{"t1", "t2"}, rawTicketIDs(sd))
	assert.ElementsMatch(t, []string{"red", "blue"}, rawPlayerTeams(sd))
	gsi := sd["gameSessionInfo"].(map[string]any)
	assert.Equal(t, sd["matchId"], gsi["matchId"])
}

func TestE2E_StopMatchmakingCancels(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, false)
	client := newGameLiftClient(t, st.httpSrv.URL)

	_, err := client.StartMatchmaking(context.Background(), &gamelift.StartMatchmakingInput{
		ConfigurationName: aws.String("cfg"),
		TicketId:          aws.String("solo"),
		Players:           []types.Player{{PlayerId: aws.String("p1")}},
	})
	require.NoError(t, err)
	_, err = client.StopMatchmaking(context.Background(), &gamelift.StopMatchmakingInput{TicketId: aws.String("solo")})
	require.NoError(t, err)

	waitForTicketStatus(t, client, "solo", "CANCELLED")
	st.sink.waitFor(t, "MatchmakingCancelled")
}

// backfillRuleSet needs teams a single new ticket cannot fill on its own, so a
// match only forms once a backfill request seats the players already in play.
const backfillRuleSet = `{
  "name": "2v2-backfill",
  "ruleLanguageVersion": "1.0",
  "algorithm": {"strategy": "exhaustiveSearch", "backfillPriority": "high"},
  "teams": [
    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
  ]
}`

// seatedPlayers is the roster of a 2v2 session with one blue seat empty, as a
// game server would report it to StartMatchBackfill.
func seatedPlayers() []types.Player {
	return []types.Player{
		{PlayerId: aws.String("p1"), Team: aws.String("red")},
		{PlayerId: aws.String("p2"), Team: aws.String("red")},
		{PlayerId: aws.String("p3"), Team: aws.String("blue")},
	}
}

func TestE2E_BackfillFillsEmptySeat(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStackWith(t, "2v2-backfill", backfillRuleSet, false)
	client := newGameLiftClient(t, st.httpSrv.URL)

	out, err := client.StartMatchBackfill(context.Background(), &gamelift.StartMatchBackfillInput{
		ConfigurationName: aws.String("cfg"),
		TicketId:          aws.String("bf1"),
		GameSessionArn:    aws.String("arn:aws:gamelift:us-east-1:000000000000:gamesession/gs-1"),
		Players:           seatedPlayers(),
	})
	require.NoError(t, err)
	require.NotNil(t, out.MatchmakingTicket)
	assert.Equal(t, "bf1", aws.ToString(out.MatchmakingTicket.TicketId))
	assert.Equal(t, types.MatchmakingConfigurationStatusQueued, out.MatchmakingTicket.Status)
	// The teams the request declared come straight back, as they do on AWS.
	require.Len(t, out.MatchmakingTicket.Players, 3)
	assert.Equal(t, "red", aws.ToString(out.MatchmakingTicket.Players[0].Team))

	_, err = client.StartMatchmaking(context.Background(), &gamelift.StartMatchmakingInput{
		ConfigurationName: aws.String("cfg"),
		TicketId:          aws.String("t1"),
		Players:           []types.Player{{PlayerId: aws.String("p4")}},
	})
	require.NoError(t, err)

	for _, id := range []string{"bf1", "t1"} {
		waitForTicketStatus(t, client, id, "COMPLETED")
	}
	st.sink.waitFor(t, "MatchmakingSucceeded")

	// The backfill ticket rides the ordinary event path: one searching event
	// each, then a single match-level success carrying the whole session.
	require.Len(t, st.sink.rawEnvelopesOfType("MatchmakingSearching"), 2)
	succeededs := st.sink.rawEnvelopesOfType("MatchmakingSucceeded")
	require.Len(t, succeededs, 1)
	sd := succeededs[0]["detail"].(map[string]any)
	assertDetailShape(t, sd, eventShape{
		detailKeys:     []string{"type", "matchId", "tickets", "gameSessionInfo"},
		playerRequired: []string{"playerId", "team"},
		gsiKeys:        []string{"players", "matchId"},
	})
	assert.ElementsMatch(t, []string{"bf1", "t1"}, rawTicketIDs(sd))
	// Everyone in the session is reported, the newcomer on the free blue seat.
	assert.ElementsMatch(t, []string{"red", "red", "blue", "blue"}, rawPlayerTeams(sd))
}

func TestE2E_BackfillSupersedesEarlierRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStackWith(t, "2v2-backfill", backfillRuleSet, false)
	client := newGameLiftClient(t, st.httpSrv.URL)

	gsARN := aws.String("arn:aws:gamelift:us-east-1:000000000000:gamesession/gs-1")
	for _, id := range []string{"bf1", "bf2"} {
		_, err := client.StartMatchBackfill(context.Background(), &gamelift.StartMatchBackfillInput{
			ConfigurationName: aws.String("cfg"),
			TicketId:          aws.String(id),
			GameSessionArn:    gsARN,
			Players:           seatedPlayers(),
		})
		require.NoError(t, err, "start backfill %s", id)
	}

	// One outstanding request per game session: the newer one displaces the
	// older, which ends CANCELLED with a reason that says so.
	cancelled := waitForTicketStatus(t, client, "bf1", "CANCELLED")
	assert.Contains(t, aws.ToString(cancelled.StatusMessage), "Superseded")
	waitForTicketStatus(t, client, "bf2", "QUEUED", "SEARCHING")
	st.sink.waitFor(t, "MatchmakingCancelled")
}

func TestE2E_BackfillRefusedWhileMatchAwaitsAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStackWith(t, "2v2-backfill-accept", backfillRuleSet, true)
	client := newGameLiftClient(t, st.httpSrv.URL)

	gsARN := aws.String("arn:aws:gamelift:us-east-1:000000000000:gamesession/gs-1")
	_, err := client.StartMatchBackfill(context.Background(), &gamelift.StartMatchBackfillInput{
		ConfigurationName: aws.String("cfg"),
		TicketId:          aws.String("bf1"),
		GameSessionArn:    gsARN,
		Players:           seatedPlayers(),
	})
	require.NoError(t, err)
	_, err = client.StartMatchmaking(context.Background(), &gamelift.StartMatchmakingInput{
		ConfigurationName: aws.String("cfg"),
		TicketId:          aws.String("t1"),
		Players:           []types.Player{{PlayerId: aws.String("p4")}},
	})
	require.NoError(t, err)
	waitForTicketStatus(t, client, "bf1", "REQUIRES_ACCEPTANCE")

	// fmlocal refuses to replace a request already in a proposal rather than
	// cancel the sibling ticket out from under it.
	_, err = client.StartMatchBackfill(context.Background(), &gamelift.StartMatchBackfillInput{
		ConfigurationName: aws.String("cfg"),
		TicketId:          aws.String("bf2"),
		GameSessionArn:    gsARN,
		Players:           seatedPlayers(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidRequestException")

	// The game server accepts for the players it already has seated, as it must
	// against AWS, and the match completes.
	_, err = client.AcceptMatch(context.Background(), &gamelift.AcceptMatchInput{
		TicketId:       aws.String("bf1"),
		PlayerIds:      []string{"p1", "p2", "p3"},
		AcceptanceType: types.AcceptanceTypeAccept,
	})
	require.NoError(t, err)
	_, err = client.AcceptMatch(context.Background(), &gamelift.AcceptMatchInput{
		TicketId:       aws.String("t1"),
		PlayerIds:      []string{"p4"},
		AcceptanceType: types.AcceptanceTypeAccept,
	})
	require.NoError(t, err)
	for _, id := range []string{"bf1", "t1"} {
		waitForTicketStatus(t, client, id, "COMPLETED")
	}
	st.sink.waitFor(t, "AcceptMatchCompleted")
	st.sink.waitFor(t, "MatchmakingSucceeded")
}

func TestE2E_BackfillRejectsPlayerWithoutTeam(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStackWith(t, "2v2-backfill", backfillRuleSet, false)
	client := newGameLiftClient(t, st.httpSrv.URL)
	_, err := client.StartMatchBackfill(context.Background(), &gamelift.StartMatchBackfillInput{
		ConfigurationName: aws.String("cfg"),
		Players:           []types.Player{{PlayerId: aws.String("p1")}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidRequestException")
}

func TestE2E_StopMatchBackfillIsUnknownOperation(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, false)
	// StopMatchBackfill is not a GameLift operation — backfill tickets are
	// stopped with StopMatchmaking — so it is not in the SDK and reaching the
	// server under that target must answer as AWS does.
	req, err := http.NewRequest("POST", st.httpSrv.URL+"/", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "GameLift.StopMatchBackfill")
	resp, err := st.httpSrv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, 400, resp.StatusCode)
	assert.Contains(t, string(body), "UnknownOperationException")
}
