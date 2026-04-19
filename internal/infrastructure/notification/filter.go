package notification

import (
	"context"

	"github.com/moepig/fmlocal/internal/app/ports"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

// Filtered wraps an EventPublisher and forwards only events whose name is in
// the allow set. It is the implementation of the YAML `onlyEvents` option.
type Filtered struct {
	inner ports.EventPublisher
	allow map[string]struct{}
}

// NewFiltered returns a publisher that delegates to inner only when the event
// name is in names. If names is empty, every call passes through (equivalent
// to not wrapping at all).
func NewFiltered(inner ports.EventPublisher, names []string) *Filtered {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return &Filtered{inner: inner, allow: set}
}

func (f *Filtered) Publish(ctx context.Context, e mm.Event) error {
	if len(f.allow) > 0 {
		if _, ok := f.allow[e.EventName()]; !ok {
			return nil
		}
	}
	return f.inner.Publish(ctx, e)
}

var _ ports.EventPublisher = (*Filtered)(nil)
