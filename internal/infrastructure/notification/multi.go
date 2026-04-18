package notification

import (
	"context"
	"errors"

	"github.com/moepig/fmlocal/internal/app/ports"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

// Multi fans a single event out to multiple publishers. Child failures are
// joined into one error and do not abort subsequent children.
type Multi struct {
	children []ports.EventPublisher
}

func NewMulti(children ...ports.EventPublisher) *Multi {
	return &Multi{children: children}
}

func (m *Multi) Publish(ctx context.Context, e mm.Event) error {
	var errs []error
	for _, p := range m.children {
		if err := p.Publish(ctx, e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Noop discards every event. Useful as a default when no publishers are
// configured for a given matchmaking configuration.
type Noop struct{}

func (Noop) Publish(context.Context, mm.Event) error { return nil }

var (
	_ ports.EventPublisher = (*Multi)(nil)
	_ ports.EventPublisher = Noop{}
)
