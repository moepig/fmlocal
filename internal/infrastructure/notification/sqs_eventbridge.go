package notification

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/moepig/fmlocal/internal/app/ports"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

// SQSClient is the minimum *sqs.Client surface needed by the publisher.
//
//go:generate mockgen -destination=mocks/mock_sqs.go -package=mocks . SQSClient
type SQSClient interface {
	SendMessage(ctx context.Context, in *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

type SQSEventBridgePublisher struct {
	QueueURL   string
	Translator *Translator
	Client     SQSClient
}

func NewSQSEventBridgePublisher(queueURL string, translator *Translator, client SQSClient) *SQSEventBridgePublisher {
	return &SQSEventBridgePublisher{QueueURL: queueURL, Translator: translator, Client: client}
}

func (p *SQSEventBridgePublisher) Publish(ctx context.Context, e mm.Event) error {
	body, err := p.Translator.Marshal(e)
	if err != nil {
		return err
	}
	_, err = p.Client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(p.QueueURL),
		MessageBody: aws.String(string(body)),
	})
	if err != nil {
		return fmt.Errorf("notification: sqs send: %w", err)
	}
	return nil
}

var _ ports.EventPublisher = (*SQSEventBridgePublisher)(nil)
