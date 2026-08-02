package awsapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

const testRuleSet = `{
  "name": "1v1",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [{"name": "skill", "type": "number"}],
  "teams": [
    {"name": "red",  "minPlayers": 1, "maxPlayers": 1},
    {"name": "blue", "minPlayers": 1, "maxPlayers": 1}
  ]
}`

type harness struct {
	httpSrv *httptest.Server
	svc     *appmm.Service
}

// bigTeamRuleSet has room for AWS's 199-player backfill limit, which the 1v1
// set used elsewhere would reject on team size long before the limit is
// reached.
const bigTeamRuleSet = `{
  "name": "1v1",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [{"name": "skill", "type": "number"}],
  "teams": [
    {"name": "red",  "minPlayers": 1, "maxPlayers": 100},
    {"name": "blue", "minPlayers": 1, "maxPlayers": 100}
  ]
}`

func setup(t *testing.T) *harness {
	t.Helper()
	return setupWithRuleSet(t, testRuleSet)
}

func setupWithRuleSet(t *testing.T, ruleSet string) *harness {
	t.Helper()
	clk := sysclock.NewFake(time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC))
	cfg := mm.Configuration{
		Name:           "c1",
		ARN:            "arn:aws:gamelift:us-east-1:000000000000:matchmakingconfiguration/c1",
		RuleSetName:    "1v1",
		RuleSetARN:     "arn:aws:gamelift:us-east-1:000000000000:matchmakingruleset/1v1",
		FlexMatchMode:  mm.FlexMatchModeStandalone,
		RequestTimeout: 60 * time.Second,
	}
	rs := mm.RuleSet{Name: "1v1", ARN: cfg.RuleSetARN, Body: []byte(ruleSet)}

	engine, err := appmm.BuildEngine(cfg, rs, flexi.WithClock(clk))
	require.NoError(t, err)
	resolver := appmm.NewStaticEngineResolver()
	resolver.Register(cfg.Name, engine)

	svc := &appmm.Service{
		Engines:    resolver,
		Publishers: map[mm.ConfigurationName]ports.EventPublisher{cfg.Name: notification.Noop{}},
		Clock:      clk,
		IDs:        idgen.NewSequence("ticket-"),
		MatchIDs:   idgen.NewSequence("match-"),
	}
	svc.LoadConfigurations([]mm.Configuration{cfg})
	svc.LoadRuleSets([]mm.RuleSet{rs})
	apiSrv := awsapi.NewServer(svc, awsapi.Options{}, nil)
	h := &harness{httpSrv: httptest.NewServer(apiSrv.Handler()), svc: svc}
	t.Cleanup(h.httpSrv.Close)
	return h
}

func call(t *testing.T, srv *httptest.Server, action, payload string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest("POST", srv.URL+"/", bytes.NewReader([]byte(payload)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "GameLift."+action)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func TestDispatcher_UnknownActionReturnsUnknownOperation(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "MysteryOp", `{}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "UnknownOperationException")
}

func TestStartMatchmaking_AllocatesTicketID(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "StartMatchmaking", `{
	  "ConfigurationName": "c1",
	  "Players": [{"PlayerId": "p1", "PlayerAttributes": {"skill": {"N": 50}}}]
	}`)
	assert.Equal(t, 200, code)
	var out awsapi.StartMatchmakingOutput
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Equal(t, "ticket-1", out.MatchmakingTicket.TicketID)
	assert.Equal(t, "QUEUED", out.MatchmakingTicket.Status)
}

func TestStartMatchmaking_AttributeTypeMismatchIsInvalidRequest(t *testing.T) {
	h := setup(t)
	// "skill" is declared as number in the rule set; a string value must be a
	// client error (400 InvalidRequestException, matching AWS), not a 500.
	code, body := call(t, h.httpSrv, "StartMatchmaking", `{
	  "ConfigurationName": "c1",
	  "Players": [{"PlayerId": "p1", "PlayerAttributes": {"skill": {"S": "high"}}}]
	}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "InvalidRequestException")
}

func TestStartMatchmaking_TeamIsRejected(t *testing.T) {
	h := setup(t)
	// AWS: "Do not specify a team if you are not using backfill"; a Team on a
	// regular StartMatchmaking request is a client error, not silently ignored.
	code, body := call(t, h.httpSrv, "StartMatchmaking", `{
	  "ConfigurationName": "c1",
	  "Players": [{"PlayerId": "p1", "Team": "red"}]
	}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "InvalidRequestException")
}

func TestStartMatchmaking_MoreThanTenPlayersIsRejected(t *testing.T) {
	h := setup(t)
	players := make([]string, 11)
	for i := range players {
		players[i] = `{"PlayerId": "p` + string(rune('a'+i)) + `"}`
	}
	// AWS caps StartMatchmaking at 10 players per request.
	code, body := call(t, h.httpSrv, "StartMatchmaking", `{
	  "ConfigurationName": "c1",
	  "Players": [`+strings.Join(players, ",")+`]
	}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "InvalidRequestException")
}

func TestStartMatchmaking_UnknownFieldsAreIgnored(t *testing.T) {
	h := setup(t)
	// The AWS JSON protocol ignores unknown members; a newer SDK (or a sloppy
	// client) sending extra fields must not fail against the emulator.
	code, body := call(t, h.httpSrv, "StartMatchmaking", `{
	  "ConfigurationName": "c1",
	  "FutureSDKField": true,
	  "Players": [{"PlayerId": "p1"}]
	}`)
	assert.Equal(t, 200, code, "body: %s", body)
}

func TestStartMatchmaking_InvalidTicketIDIsRejected(t *testing.T) {
	h := setup(t)
	// AWS constrains TicketId to [a-zA-Z0-9-\.]* (max 128 chars). Rejecting
	// anything else also rules out ids containing "|", which the proposal
	// tracker uses as its roster-key separator.
	for name, id := range map[string]string{
		"pipe":     "bad|id",
		"space":    "bad id",
		"unicode":  "チケット",
		"too-long": strings.Repeat("a", 129),
	} {
		code, body := call(t, h.httpSrv, "StartMatchmaking", `{
		  "ConfigurationName": "c1", "TicketId": `+toJSONString(id)+`,
		  "Players": [{"PlayerId": "p1"}]
		}`)
		assert.Equal(t, 400, code, "case %s", name)
		assert.Contains(t, string(body), "InvalidRequestException", "case %s", name)
	}
}

func TestStartMatchmaking_UnknownConfigurationIsNotFound(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "StartMatchmaking", `{
	  "ConfigurationName": "ghost",
	  "Players": [{"PlayerId": "p1"}]
	}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "NotFoundException")
}

func TestDescribeMatchmaking_RoundTrip(t *testing.T) {
	h := setup(t)
	call(t, h.httpSrv, "StartMatchmaking", `{
	  "ConfigurationName": "c1", "TicketId": "tk1",
	  "Players": [{"PlayerId": "p1"}]
	}`)
	code, body := call(t, h.httpSrv, "DescribeMatchmaking", `{"TicketIds": ["tk1"]}`)
	require.Equal(t, 200, code)
	var out awsapi.DescribeMatchmakingOutput
	require.NoError(t, json.Unmarshal(body, &out))
	require.Len(t, out.TicketList, 1)
	assert.Equal(t, "QUEUED", out.TicketList[0].Status)
}

func TestStopMatchmaking_CancelsTicket(t *testing.T) {
	h := setup(t)
	call(t, h.httpSrv, "StartMatchmaking", `{
	  "ConfigurationName": "c1", "TicketId": "tk1",
	  "Players": [{"PlayerId": "p1"}]
	}`)
	code, _ := call(t, h.httpSrv, "StopMatchmaking", `{"TicketId": "tk1"}`)
	require.Equal(t, 200, code)
	require.NoError(t, h.svc.Tick(t.Context(), "c1"))
	_, body := call(t, h.httpSrv, "DescribeMatchmaking", `{"TicketIds": ["tk1"]}`)
	var out awsapi.DescribeMatchmakingOutput
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Equal(t, "CANCELLED", out.TicketList[0].Status)
}

func TestStartMatchBackfill_QueuesTicketWithTeams(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "StartMatchBackfill", `{
	  "ConfigurationName": "c1",
	  "GameSessionArn": "arn:aws:gamelift:us-east-1:000000000000:gamesession/gs-1",
	  "Players": [{"PlayerId": "p1", "Team": "red", "PlayerAttributes": {"skill": {"N": 50}}}]
	}`)
	require.Equal(t, 200, code)
	var out awsapi.StartMatchBackfillOutput
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Equal(t, "ticket-1", out.MatchmakingTicket.TicketID)
	assert.Equal(t, "QUEUED", out.MatchmakingTicket.Status)
	// AWS reports a backfill ticket's team membership from the moment the
	// request is made, not only once a match forms.
	require.Len(t, out.MatchmakingTicket.Players, 1)
	assert.Equal(t, "red", out.MatchmakingTicket.Players[0].Team)
}

func TestStartMatchBackfill_RequiresTeamOnEveryPlayer(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "StartMatchBackfill", `{
	  "ConfigurationName": "c1",
	  "Players": [{"PlayerId": "p1", "Team": "red"}, {"PlayerId": "p2"}]
	}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "InvalidRequestException")
}

func TestStartMatchBackfill_RequiresPlayers(t *testing.T) {
	h := setup(t)
	// AWS gives Players a minimum of 1 item, so an absent list violates a
	// documented constraint.
	code, body := call(t, h.httpSrv, "StartMatchBackfill", `{"ConfigurationName": "c1"}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "InvalidRequestException")
}

func TestStartMatchBackfill_UnknownConfigurationIsNotFound(t *testing.T) {
	h := setup(t)
	players := `[{"PlayerId": "p1", "Team": "red"}]`
	// ConfigurationName has no minimum length on AWS, so an empty name is a
	// well-formed one that resolves to nothing — the same answer an unknown
	// name gets, and the same StartMatchmaking gives.
	for _, name := range []string{`""`, `"ghost"`} {
		code, body := call(t, h.httpSrv, "StartMatchBackfill", `{"ConfigurationName": `+name+`, "Players": `+players+`}`)
		assert.Equal(t, 400, code, name)
		assert.Contains(t, string(body), "NotFoundException", name)
	}
	for _, name := range []string{`""`, `"ghost"`} {
		code, body := call(t, h.httpSrv, "StartMatchmaking", `{"ConfigurationName": `+name+`, "Players": [{"PlayerId": "p1"}]}`)
		assert.Equal(t, 400, code, name)
		assert.Contains(t, string(body), "NotFoundException", name)
	}
}

func TestStartMatchBackfill_MoreThan199PlayersIsRejected(t *testing.T) {
	h := setup(t)
	players := make([]string, 200)
	for i := range players {
		players[i] = fmt.Sprintf(`{"PlayerId": "p%d", "Team": "red"}`, i)
	}
	// AWS caps StartMatchBackfill's Players at 199.
	code, body := call(t, h.httpSrv, "StartMatchBackfill", `{
	  "ConfigurationName": "c1",
	  "Players": [`+strings.Join(players, ",")+`]
	}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "InvalidRequestException")
}

func TestStartMatchBackfill_Exactly199PlayersIsAccepted(t *testing.T) {
	h := setupWithRuleSet(t, bigTeamRuleSet)
	players := make([]string, 199)
	for i := range players {
		team := "red"
		if i >= 100 {
			team = "blue"
		}
		players[i] = fmt.Sprintf(`{"PlayerId": "p%d", "Team": %q}`, i, team)
	}
	// 199 is the limit itself, so a session that full is still refillable.
	code, body := call(t, h.httpSrv, "StartMatchBackfill", `{
	  "ConfigurationName": "c1",
	  "Players": [`+strings.Join(players, ",")+`]
	}`)
	require.Equal(t, 200, code, string(body))
	var out awsapi.StartMatchBackfillOutput
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Len(t, out.MatchmakingTicket.Players, 199)
}

func TestStartMatchBackfill_InvalidTicketIDIsRejected(t *testing.T) {
	h := setup(t)
	// The same constraint StartMatchmaking enforces: AWS's [a-zA-Z0-9-.]* over
	// at most 128 characters. Keeping "|" out of ticket ids also keeps the
	// proposal tracker's composite key unambiguous.
	for _, id := range []string{"bf|1", "bf 1", strings.Repeat("b", 129)} {
		code, body := call(t, h.httpSrv, "StartMatchBackfill", `{
		  "ConfigurationName": "c1",
		  "TicketId": "`+id+`",
		  "Players": [{"PlayerId": "p1", "Team": "red"}]
		}`)
		assert.Equal(t, 400, code, id)
		assert.Contains(t, string(body), "InvalidRequestException", id)
	}
}

func TestStartMatchBackfill_UnknownTeamIsInvalidRequest(t *testing.T) {
	h := setup(t)
	// The rule set declares red and blue only. flexi rejects the roster with
	// ErrInvalidTicket, which must surface as the caller's mistake, not a 500.
	code, body := call(t, h.httpSrv, "StartMatchBackfill", `{
	  "ConfigurationName": "c1",
	  "Players": [{"PlayerId": "p1", "Team": "green"}]
	}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "InvalidRequestException")
}

func TestDescribeMatchmakingRuleSets_ReturnsBody(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "DescribeMatchmakingRuleSets", `{"Names": ["1v1"]}`)
	require.Equal(t, 200, code)
	var out awsapi.DescribeMatchmakingRuleSetsOutput
	require.NoError(t, json.Unmarshal(body, &out))
	require.Len(t, out.RuleSets, 1)
}

func TestValidateRuleSet_AcceptsValid(t *testing.T) {
	h := setup(t)
	payload := `{"RuleSetBody": ` + toJSONString(testRuleSet) + `}`
	code, body := call(t, h.httpSrv, "ValidateMatchmakingRuleSet", payload)
	require.Equal(t, 200, code)
	var out awsapi.ValidateMatchmakingRuleSetOutput
	require.NoError(t, json.Unmarshal(body, &out))
	assert.True(t, out.Valid)
}

func TestValidateRuleSet_RejectsInvalid(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "ValidateMatchmakingRuleSet", `{"RuleSetBody": "{}"}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "InvalidRequestException")
}

func TestDescribeMatchmakingConfigurations_All(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "DescribeMatchmakingConfigurations", `{}`)
	require.Equal(t, 200, code)
	var out awsapi.DescribeMatchmakingConfigurationsOutput
	require.NoError(t, json.Unmarshal(body, &out))
	require.Len(t, out.Configurations, 1)
	assert.Equal(t, "c1", out.Configurations[0].Name)
	assert.Equal(t, "STANDALONE", out.Configurations[0].FlexMatchMode)
}

func TestDescribeMatchmakingConfigurations_FilterByName(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "DescribeMatchmakingConfigurations", `{"Names": ["missing"]}`)
	require.Equal(t, 200, code)
	var out awsapi.DescribeMatchmakingConfigurationsOutput
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Len(t, out.Configurations, 0)
}

func TestDescribeMatchmakingConfigurations_FilterByRuleSet(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "DescribeMatchmakingConfigurations", `{"RuleSetName": "1v1"}`)
	require.Equal(t, 200, code)
	var out awsapi.DescribeMatchmakingConfigurationsOutput
	require.NoError(t, json.Unmarshal(body, &out))
	require.Len(t, out.Configurations, 1)
	assert.Equal(t, "c1", out.Configurations[0].Name)

	code, body = call(t, h.httpSrv, "DescribeMatchmakingConfigurations", `{"RuleSetName": "other"}`)
	require.Equal(t, 200, code)
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Len(t, out.Configurations, 0)
}

func TestDescribeMatchmakingRuleSets_All(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "DescribeMatchmakingRuleSets", `{}`)
	require.Equal(t, 200, code)
	var out awsapi.DescribeMatchmakingRuleSetsOutput
	require.NoError(t, json.Unmarshal(body, &out))
	require.Len(t, out.RuleSets, 1)
	assert.Equal(t, "1v1", out.RuleSets[0].RuleSetName)
	assert.NotEmpty(t, out.RuleSets[0].RuleSetBody)
}

func TestAcceptMatch_RequiresTicketId(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "AcceptMatch", `{"AcceptanceType":"ACCEPT","PlayerIds":["p1"]}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "InvalidRequestException")
}

func TestAcceptMatch_InvalidAcceptanceType(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "AcceptMatch", `{"TicketId":"tk1","AcceptanceType":"MAYBE","PlayerIds":["p1"]}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "InvalidRequestException")
}

func TestAcceptMatch_TicketNotFound(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "AcceptMatch", `{"TicketId":"ghost","AcceptanceType":"ACCEPT","PlayerIds":["p1"]}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "NotFoundException")
}

func TestAcceptMatch_Success(t *testing.T) {
	h := setup(t)
	// Need two players to form a proposal with acceptance.
	const acceptRS = `{
	  "name": "1v1-accept",
	  "ruleLanguageVersion": "1.0",
	  "teams": [
	    {"name": "red",  "minPlayers": 1, "maxPlayers": 1},
	    {"name": "blue", "minPlayers": 1, "maxPlayers": 1}
	  ],
	  "acceptanceRequired": true,
	  "acceptanceTimeoutSeconds": 30
	}`
	clk := sysclock.NewFake(time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC))
	cfg2 := mm.Configuration{
		Name: "accept", RuleSetName: "1v1-accept", ARN: "arn:aws:gamelift:us-east-1:000000000000:matchmakingconfiguration/accept",
		FlexMatchMode: mm.FlexMatchModeStandalone, RequestTimeout: 60 * time.Second,
		AcceptanceRequired: true, AcceptanceTimeout: 30 * time.Second,
	}
	engine2, err := appmm.BuildEngine(cfg2, mm.RuleSet{Name: "1v1-accept", Body: []byte(acceptRS)}, flexi.WithClock(clk))
	require.NoError(t, err)
	h.svc.Engines.(*appmm.StaticEngineResolver).Register(cfg2.Name, engine2)
	h.svc.LoadConfigurations([]mm.Configuration{
		{Name: "c1", ARN: "arn:...", RuleSetName: "1v1", FlexMatchMode: mm.FlexMatchModeStandalone, RequestTimeout: 60 * time.Second},
		cfg2,
	})

	for _, id := range []string{"ta", "tb"} {
		code, _ := call(t, h.httpSrv, "StartMatchmaking", `{
		  "ConfigurationName": "accept", "TicketId": "`+id+`",
		  "Players": [{"PlayerId": "p-`+id+`"}]
		}`)
		require.Equal(t, 200, code)
	}
	require.NoError(t, h.svc.Tick(t.Context(), "accept"))

	code, _ := call(t, h.httpSrv, "AcceptMatch", `{
	  "TicketId": "ta", "AcceptanceType": "ACCEPT", "PlayerIds": ["p-ta"]
	}`)
	assert.Equal(t, 200, code)
}

func TestStopMatchmaking_TicketNotFound(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "StopMatchmaking", `{"TicketId": "ghost"}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "NotFoundException")
}

func TestStopMatchBackfill_IsUnknownOperation(t *testing.T) {
	h := setup(t)
	// StopMatchBackfill does not exist in the GameLift API (backfill tickets
	// are stopped via StopMatchmaking), so real AWS answers with
	// UnknownOperationException.
	code, body := call(t, h.httpSrv, "StopMatchBackfill", `{}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "UnknownOperationException")
}

func toJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
