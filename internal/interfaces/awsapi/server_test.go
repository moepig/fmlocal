package awsapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func setup(t *testing.T) *harness {
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
	rs := mm.RuleSet{Name: "1v1", ARN: cfg.RuleSetARN, Body: []byte(testRuleSet)}
	
	engine, err := flexi.New(rs.Body, flexi.WithClock(clk))
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

func TestStartMatchBackfill_IsUnsupported(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "StartMatchBackfill", `{"ConfigurationName": "c1"}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "UnsupportedOperationException")
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
	engine2, err := flexi.New([]byte(acceptRS), flexi.WithClock(clk))
	require.NoError(t, err)
	cfg2 := mm.Configuration{
		Name: "accept", RuleSetName: "1v1-accept", ARN: "arn:aws:gamelift:us-east-1:000000000000:matchmakingconfiguration/accept",
		FlexMatchMode: mm.FlexMatchModeStandalone, RequestTimeout: 60 * time.Second,
		AcceptanceRequired: true, AcceptanceTimeout: 30 * time.Second,
	}
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

func TestStopMatchbackfill_IsUnsupported(t *testing.T) {
	h := setup(t)
	code, body := call(t, h.httpSrv, "StopMatchBackfill", `{}`)
	assert.Equal(t, 400, code)
	assert.Contains(t, string(body), "UnsupportedOperationException")
}

func toJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
