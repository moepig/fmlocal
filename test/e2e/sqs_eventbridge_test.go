package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	"github.com/aws/aws-sdk-go-v2/service/gamelift/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

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
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestE2E_SQSEventBridge_RealElasticMQ(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test with testcontainers (run without -short)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	req := testcontainers.ContainerRequest{
		Image:        "softwaremill/elasticmq-native:latest",
		ExposedPorts: []string{"9324/tcp"},
		WaitingFor:   wait.ForListeningPort("9324/tcp").WithStartupTimeout(60 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "9324/tcp")
	require.NoError(t, err)
	endpoint := fmt.Sprintf("http://%s:%s", host, port.Port())

	sqsClient := newSQSClient(t, ctx, endpoint)
	createOut, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("fmlocal")})
	require.NoError(t, err)
	queueURL := aws.ToString(createOut.QueueUrl)

	stack := buildSQSBackedStack(t, queueURL, sqsClient)
	client := newGameLiftClientLocal(t, stack.httpSrv.URL)

	_, err = client.StartMatchmaking(ctx, &gamelift.StartMatchmakingInput{
		ConfigurationName: aws.String("cfg"),
		TicketId:          aws.String("tk1"),
		Players:           []types.Player{{PlayerId: aws.String("p1")}},
	})
	require.NoError(t, err)
	_, err = client.StartMatchmaking(ctx, &gamelift.StartMatchmakingInput{
		ConfigurationName: aws.String("cfg"),
		TicketId:          aws.String("tk2"),
		Players:           []types.Player{{PlayerId: aws.String("p2")}},
	})
	require.NoError(t, err)

	deadline := time.Now().Add(15 * time.Second)
	byType := map[string][]map[string]any{} // raw envelopes delivered over SQS, keyed by detail.type
	for time.Now().Before(deadline) {
		out, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     1,
		})
		require.NoError(t, err)
		for _, m := range out.Messages {
			var env map[string]any
			require.NoError(t, json.Unmarshal([]byte(aws.ToString(m.Body)), &env))
			typ := env["detail"].(map[string]any)["type"].(string)
			byType[typ] = append(byType[typ], env)
			_, _ = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      aws.String(queueURL),
				ReceiptHandle: m.ReceiptHandle,
			})
		}
		if len(byType["MatchmakingSucceeded"]) > 0 {
			break
		}
	}

	// The message bodies delivered over real SQS must carry the full element set
	// — identical to the HTTP path, since both share the translator's Marshal.
	require.NotEmpty(t, byType["MatchmakingSearching"], "got %v", keySet(toAnyMap(byType)))
	require.NotEmpty(t, byType["PotentialMatchCreated"])
	require.NotEmpty(t, byType["MatchmakingSucceeded"])

	searchShape := eventShape{
		detailKeys:     []string{"type", "tickets", "estimatedWaitMillis", "gameSessionInfo"},
		playerRequired: []string{"playerId"},
		gsiKeys:        []string{"players"},
	}
	for _, env := range byType["MatchmakingSearching"] {
		assertEnvelopeShape(t, env)
		d := env["detail"].(map[string]any)
		assertDetailShape(t, d, searchShape)
		assert.Equal(t, "NOT_AVAILABLE", d["estimatedWaitMillis"])
	}

	pmcShape := eventShape{
		detailKeys:     []string{"type", "matchId", "tickets", "acceptanceRequired", "gameSessionInfo"},
		playerRequired: []string{"playerId", "team"},
		gsiKeys:        []string{"players"},
	}
	pmc := byType["PotentialMatchCreated"][0]
	assertEnvelopeShape(t, pmc)
	pd := pmc["detail"].(map[string]any)
	assertDetailShape(t, pd, pmcShape)
	assert.Equal(t, false, pd["acceptanceRequired"])
	assert.ElementsMatch(t, []string{"tk1", "tk2"}, rawTicketIDs(pd))
	assert.ElementsMatch(t, []string{"red", "blue"}, rawPlayerTeams(pd))

	succShape := eventShape{
		detailKeys:     []string{"type", "matchId", "tickets", "gameSessionInfo"},
		playerRequired: []string{"playerId", "team"},
		gsiKeys:        []string{"players", "matchId"},
	}
	succ := byType["MatchmakingSucceeded"][0]
	assertEnvelopeShape(t, succ)
	sd := succ["detail"].(map[string]any)
	assertDetailShape(t, sd, succShape)
	assert.NotEmpty(t, sd["matchId"])
	assert.ElementsMatch(t, []string{"tk1", "tk2"}, rawTicketIDs(sd))
	assert.ElementsMatch(t, []string{"red", "blue"}, rawPlayerTeams(sd))
	assert.Equal(t, sd["matchId"], sd["gameSessionInfo"].(map[string]any)["matchId"])
}

// toAnyMap adapts a typed map for keySet in diagnostic messages.
func toAnyMap[V any](m map[string]V) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func newSQSClient(t *testing.T, ctx context.Context, endpoint string) *sqs.Client {
	t.Helper()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("x", "x", "")),
	)
	require.NoError(t, err)
	return sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

func newGameLiftClientLocal(t *testing.T, endpoint string) *gamelift.Client {
	return newGameLiftClient(t, endpoint)
}

type sqsStack struct {
	httpSrv *httptest.Server
}

func buildSQSBackedStack(t *testing.T, queueURL string, client *sqs.Client) *sqsStack {
	t.Helper()
	clk := sysclock.System{}
	ids := idgen.NewUUID()
	rsBody := []byte(`{
	  "name": "1v1",
	  "ruleLanguageVersion": "1.0",
	  "teams": [
	    {"name": "red",  "minPlayers": 1, "maxPlayers": 1},
	    {"name": "blue", "minPlayers": 1, "maxPlayers": 1}
	  ]
	}`)
	cfg := mm.Configuration{
		Name:              "cfg",
		ARN:               "arn:aws:gamelift:us-east-1:000000000000:matchmakingconfiguration/cfg",
		RuleSetName:       "1v1",
		RuleSetARN:        "arn:aws:gamelift:us-east-1:000000000000:matchmakingruleset/1v1",
		FlexMatchMode:     mm.FlexMatchModeStandalone,
		RequestTimeout:    60 * time.Second,
		NotificationTargetIDs: []string{"bus"},
	}
	rs := mm.RuleSet{Name: "1v1", ARN: cfg.RuleSetARN, Body: rsBody}
	
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
	publisher := notification.NewSQSEventBridgePublisher(queueURL, translator, client)
	svc.Publishers = map[mm.ConfigurationName]ports.EventPublisher{cfg.Name: publisher}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go (&appmm.Ticker{Service: svc, Names: []mm.ConfigurationName{cfg.Name}}).Run(ctx, 50*time.Millisecond)

	apiSrv := awsapi.NewServer(svc, awsapi.Options{}, nil)
	httpSrv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(httpSrv.Close)
	return &sqsStack{httpSrv: httpSrv}
}
