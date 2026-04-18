package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/moepig/fmlocal/internal/app/ports"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

// HTTPDoer mirrors *http.Client for dependency injection in tests.
//
//go:generate mockgen -destination=mocks/mock_httpdoer.go -package=mocks . HTTPDoer
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// SNSNotification is the payload shape AWS SNS delivers to HTTP subscribers.
type SNSNotification struct {
	Type             string `json:"Type"`
	MessageID        string `json:"MessageId"`
	TopicARN         string `json:"TopicArn"`
	Subject          string `json:"Subject,omitempty"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	UnsubscribeURL   string `json:"UnsubscribeURL"`
}

// placeholderTopicARN is the TopicArn fmlocal stamps into every outgoing SNS
// envelope. fmlocal is not a real SNS topic, so the value is arbitrary; it
// exists only so consumers that branch on TopicArn see a well-formed string.
const placeholderTopicARN = "arn:aws:sns:local:000000000000:fmlocal"

type SNSHTTPPublisher struct {
	URL        string
	Translator *Translator
	IDs        ports.IDGenerator
	Doer       HTTPDoer
}

func NewSNSHTTPPublisher(url string, translator *Translator, ids ports.IDGenerator, doer HTTPDoer) *SNSHTTPPublisher {
	if doer == nil {
		doer = http.DefaultClient
	}
	return &SNSHTTPPublisher{URL: url, Translator: translator, IDs: ids, Doer: doer}
}

func (p *SNSHTTPPublisher) Publish(ctx context.Context, e mm.Event) error {
	envelope, err := p.Translator.Marshal(e)
	if err != nil {
		return err
	}
	note := SNSNotification{
		Type:             "Notification",
		MessageID:        p.IDs.NewID(),
		TopicARN:         placeholderTopicARN,
		Message:          string(envelope),
		Timestamp:        e.OccurredAt().UTC().Format(time.RFC3339Nano),
		SignatureVersion: "1",
		Signature:        "fmlocal-unsigned",
		SigningCertURL:   "http://fmlocal.local/cert.pem",
		UnsubscribeURL:   "http://fmlocal.local/unsubscribe",
	}
	body, err := json.Marshal(note)
	if err != nil {
		return fmt.Errorf("notification: marshal sns notification: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notification: build sns request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-amz-sns-message-type", "Notification")
	req.Header.Set("x-amz-sns-message-id", note.MessageID)
	req.Header.Set("x-amz-sns-topic-arn", note.TopicARN)
	resp, err := p.Doer.Do(req)
	if err != nil {
		return fmt.Errorf("notification: sns http post: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("notification: sns http subscriber returned %d", resp.StatusCode)
	}
	return nil
}

var _ ports.EventPublisher = (*SNSHTTPPublisher)(nil)
