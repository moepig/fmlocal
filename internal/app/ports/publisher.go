package ports

import (
	"context"

	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

// EventPublisher delivers domain events to the outside world. Implementations
// may translate them to wire formats such as AWS EventBridge + SNS/SQS.
//
//go:generate mockgen -destination=mocks/mock_publisher.go -package=mocks . EventPublisher
type EventPublisher interface {
	Publish(ctx context.Context, e mm.Event) error
}
