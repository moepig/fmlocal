package notification_test

import (
	"context"
	"testing"

	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"github.com/moepig/fmlocal/internal/infrastructure/notification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingPub struct {
	names []string
	err   error
}

func (r *recordingPub) Publish(_ context.Context, e mm.Event) error {
	r.names = append(r.names, e.EventName())
	return r.err
}

func TestFiltered_DropsEventsNotInAllowlist(t *testing.T) {
	tk := makeTicket(t)
	searching := tk.PullEvents()[0]

	inner := &recordingPub{}
	f := notification.NewFiltered(inner, []string{mm.EventNameMatchmakingSucceeded})

	require.NoError(t, f.Publish(context.Background(), searching))
	assert.Empty(t, inner.names, "MatchmakingSearching should be filtered out")
}

func TestFiltered_ForwardsEventsInAllowlist(t *testing.T) {
	tk := makeTicket(t)
	searching := tk.PullEvents()[0]

	inner := &recordingPub{}
	f := notification.NewFiltered(inner, []string{mm.EventNameMatchmakingSearching})

	require.NoError(t, f.Publish(context.Background(), searching))
	assert.Equal(t, []string{mm.EventNameMatchmakingSearching}, inner.names)
}

func TestFiltered_EmptyAllowlistForwardsAll(t *testing.T) {
	tk := makeTicket(t)
	searching := tk.PullEvents()[0]

	inner := &recordingPub{}
	f := notification.NewFiltered(inner, nil)

	require.NoError(t, f.Publish(context.Background(), searching))
	assert.Equal(t, []string{mm.EventNameMatchmakingSearching}, inner.names)
}
