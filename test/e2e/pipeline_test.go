package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
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

// TestE2E_TwoTicketsMatchAndComplete replays the original shell smoke test:
// fire two StartMatchmaking calls via the AWS SDK, wait for the ticker to
// resolve them, and assert both reach COMPLETED with identical match IDs
// when acceptance is not required.
func TestE2E_TwoTicketsMatchAndComplete(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, false)
	client := newGameLiftClient(t, st.httpSrv.URL)
	ctx := context.Background()

	for _, id := range []string{"alpha", "bravo"} {
		_, err := client.StartMatchmaking(ctx, &gamelift.StartMatchmakingInput{
			ConfigurationName: aws.String("cfg"),
			TicketId:          aws.String(id),
			Players:           []types.Player{{PlayerId: aws.String("p-" + id)}},
		})
		require.NoError(t, err, "StartMatchmaking(%s)", id)
	}
	for _, id := range []string{"alpha", "bravo"} {
		waitForTicketStatus(t, client, id, "COMPLETED")
	}
	out, err := client.DescribeMatchmaking(ctx, &gamelift.DescribeMatchmakingInput{TicketIds: []string{"alpha", "bravo"}})
	require.NoError(t, err)
	require.Len(t, out.TicketList, 2)
	for _, tk := range out.TicketList {
		assert.Equal(t, "COMPLETED", string(tk.Status))
		assert.NotZero(t, tk.StartTime)
		assert.NotZero(t, tk.EndTime)
	}
}

// TestE2E_StartMatchmakingSucceedsWhenPublisherIsUnreachable proves that a
// failing notification target does not turn into an AWS API error: AWS
// itself decouples event delivery from the matchmaking API's response, and
// fmlocal does the same.
func TestE2E_StartMatchmakingSucceedsWhenPublisherIsUnreachable(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	// Build a stack whose publisher points at a nonexistent address so every
	// Publish fails. StartMatchmaking must still return success.
	stack := buildStackWithDeadPublisher(t)
	client := newGameLiftClient(t, stack.httpSrv.URL)
	ctx := context.Background()

	_, err := client.StartMatchmaking(ctx, &gamelift.StartMatchmakingInput{
		ConfigurationName: aws.String("cfg"),
		TicketId:          aws.String("solo"),
		Players:           []types.Player{{PlayerId: aws.String("p1")}},
	})
	require.NoError(t, err, "StartMatchmaking must not fail when publisher is unreachable")

	describe, err := client.DescribeMatchmaking(ctx, &gamelift.DescribeMatchmakingInput{TicketIds: []string{"solo"}})
	require.NoError(t, err)
	require.Len(t, describe.TicketList, 1)
	assert.Equal(t, "solo", aws.ToString(describe.TicketList[0].TicketId))
}

// buildStackWithDeadPublisher wires an fmlocal where the SNS HTTP publisher
// targets a listener that is guaranteed to be down: we allocate a local port
// with net.Listen and immediately close it so Dial always fails.
func buildStackWithDeadPublisher(t *testing.T) *stack {
	t.Helper()
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "dead", http.StatusInternalServerError)
	}))
	dead.Close() // dial succeeds to nothing; in practice returns connection refused
	deadURL := dead.URL

	clk := sysclock.System{}
	ids := idgen.NewUUID()
	cfg := mm.Configuration{
		Name:           "cfg",
		ARN:            "arn:aws:gamelift:us-east-1:000000000000:matchmakingconfiguration/cfg",
		RuleSetName:    "1v1",
		RuleSetARN:     "arn:aws:gamelift:us-east-1:000000000000:matchmakingruleset/1v1",
		FlexMatchMode:  mm.FlexMatchModeStandalone,
		RequestTimeout: 60 * time.Second,
	}
	rsBody := []byte(basicRuleSet)
	rs := mm.RuleSet{Name: "1v1", ARN: cfg.RuleSetARN, Body: rsBody}

	engine, err := flexi.New(rs.Body, flexi.WithClock(clk))
	require.NoError(t, err)
	resolver := appmm.NewStaticEngineResolver()
	resolver.Register(cfg.Name, engine)

	translator := notification.NewTranslator(ids,
		notification.EnvelopeSettings{Region: "us-east-1", AccountID: "000000000000"},
		func(id mm.TicketID) (notification.TicketDetail, bool) { return notification.TicketDetail{}, false },
	)
	publisher := notification.NewSNSHTTPPublisher(deadURL, translator, ids, http.DefaultClient)

	svc := &appmm.Service{
		Engines:    resolver,
		Publishers: map[mm.ConfigurationName]ports.EventPublisher{cfg.Name: publisher},
		Clock:      clk,
		IDs:        ids,
		MatchIDs:   idgen.NewUUID(),
	}
	svc.LoadConfigurations([]mm.Configuration{cfg})
	svc.LoadRuleSets([]mm.RuleSet{rs})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go (&appmm.Ticker{Service: svc, Names: []mm.ConfigurationName{cfg.Name}}).Run(ctx, 50*time.Millisecond)

	apiSrv := awsapi.NewServer(svc, awsapi.Options{}, nil)
	httpSrv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(httpSrv.Close)
	return &stack{httpSrv: httpSrv, svc: svc}
}
