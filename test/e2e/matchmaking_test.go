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
	mu     sync.Mutex
	events []notification.EventBridgeEnvelope
	srv    *httptest.Server
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
	sink := newEventSink(t)
	var rsBody []byte
	var rsName mm.RuleSetName
	if acceptance {
		rsBody = []byte(acceptanceRuleSet)
		rsName = "1v1-accept"
	} else {
		rsBody = []byte(basicRuleSet)
		rsName = "1v1"
	}

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

	engine, err := flexi.New(rs.Body, flexi.WithClock(clk))
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
				players = append(players, notification.PlayerDetail{PlayerID: string(p.ID)})
			}
			return notification.TicketDetail{
				TicketID:  string(tk.ID()),
				StartTime: tk.StartTime().UTC().Format(time.RFC3339),
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
	types := st.sink.detailTypes()
	assert.Contains(t, types, "MatchmakingSearching")
	assert.Contains(t, types, "MatchmakingSucceeded")
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
	got := st.sink.detailTypes()
	for _, want := range []string{
		"MatchmakingSearching",
		"PotentialMatchCreated",
		"AcceptMatch",
		"AcceptMatchCompleted",
		"MatchmakingSucceeded",
	} {
		assert.Truef(t, contains(got, want), "expected event %q in %v", want, got)
	}
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

func TestE2E_BackfillReturnsUnsupportedOperation(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, false)
	client := newGameLiftClient(t, st.httpSrv.URL)
	_, err := client.StartMatchBackfill(context.Background(), &gamelift.StartMatchBackfillInput{
		ConfigurationName: aws.String("cfg"),
		Players:           []types.Player{{PlayerId: aws.String("p1")}},
	})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "UnsupportedOperationException") || strings.Contains(err.Error(), "not supported"))
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
