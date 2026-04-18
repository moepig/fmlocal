package notification_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"github.com/moepig/fmlocal/internal/app/defaults/idgen"
	"github.com/moepig/fmlocal/internal/infrastructure/notification"
	"github.com/moepig/fmlocal/internal/infrastructure/notification/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestSNSHTTPPublisher_POSTsNotification(t *testing.T) {
	ctrl := gomock.NewController(t)
	doer := mocks.NewMockHTTPDoer(ctrl)

	var captured *http.Request
	var capturedBody []byte
	doer.EXPECT().
		Do(gomock.Any()).
		DoAndReturn(func(req *http.Request) (*http.Response, error) {
			captured = req
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			capturedBody = body
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
		})

	tk := makeTicket(t)
	ev := tk.PullEvents()[0]
	tr := notification.NewTranslator(idgen.NewSequence("env-"), notification.EnvelopeSettings{Region: "us-east-1", AccountID: "000000000000"}, lookupFor(tk))
	pub := notification.NewSNSHTTPPublisher("http://sink/endpoint", tr, idgen.NewSequence("msg-"), doer)

	require.NoError(t, pub.Publish(context.Background(), ev))
	require.NotNil(t, captured)
	assert.Equal(t, "POST", captured.Method)
	assert.Equal(t, "Notification", captured.Header.Get("x-amz-sns-message-type"))

	var note notification.SNSNotification
	require.NoError(t, json.Unmarshal(capturedBody, &note))
	var env notification.EventBridgeEnvelope
	require.NoError(t, json.Unmarshal([]byte(note.Message), &env))
	assert.Equal(t, "MatchmakingSearching", env.Detail.Type)
}

func TestSQSEventBridgePublisher_SendsBody(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockSQSClient(ctrl)

	var gotIn *sqs.SendMessageInput
	client.EXPECT().
		SendMessage(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
			gotIn = in
			return &sqs.SendMessageOutput{}, nil
		})

	tk := makeTicket(t)
	ev := tk.PullEvents()[0]
	tr := notification.NewTranslator(idgen.NewSequence("e-"), notification.EnvelopeSettings{Region: "us-east-1", AccountID: "000000000000"}, lookupFor(tk))
	pub := notification.NewSQSEventBridgePublisher("http://elasticmq:9324/queue/fmlocal", tr, client)
	require.NoError(t, pub.Publish(context.Background(), ev))
	require.NotNil(t, gotIn)
	var env notification.EventBridgeEnvelope
	require.NoError(t, json.Unmarshal([]byte(*gotIn.MessageBody), &env))
	assert.Equal(t, "aws.gamelift", env.Source)
}

type stubPub struct {
	calls int
	err   error
}

func (s *stubPub) Publish(context.Context, mm.Event) error {
	s.calls++
	return s.err
}

func TestMulti_FansOutAndCollectsErrors(t *testing.T) {
	a := &stubPub{err: errors.New("boom")}
	b := &stubPub{}
	m := notification.NewMulti(a, b)
	tk := makeTicket(t)
	err := m.Publish(context.Background(), tk.PullEvents()[0])
	require.Error(t, err)
	assert.Equal(t, 1, a.calls)
	assert.Equal(t, 1, b.calls)
}
