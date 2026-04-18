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
	foundTypes := map[string]bool{}
	for time.Now().Before(deadline) {
		out, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     1,
		})
		require.NoError(t, err)
		for _, m := range out.Messages {
			var env notification.EventBridgeEnvelope
			require.NoError(t, json.Unmarshal([]byte(aws.ToString(m.Body)), &env))
			foundTypes[env.Detail.Type] = true
			assert.Equal(t, "aws.gamelift", env.Source)
			assert.Equal(t, "GameLift Matchmaking Event", env.DetailType)
			_, _ = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      aws.String(queueURL),
				ReceiptHandle: m.ReceiptHandle,
			})
		}
		if foundTypes["MatchmakingSucceeded"] {
			break
		}
	}
	assert.True(t, foundTypes["MatchmakingSearching"], "expected MatchmakingSearching, got %v", foundTypes)
	assert.True(t, foundTypes["MatchmakingSucceeded"], "expected MatchmakingSucceeded, got %v", foundTypes)
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
				players = append(players, notification.PlayerDetail{PlayerID: string(p.ID)})
			}
			return notification.TicketDetail{
				TicketID:  string(tk.ID()),
				StartTime: tk.StartTime().UTC().Format(time.RFC3339),
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
