package matchmaking_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/moepig/flexi"
	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
	"github.com/stretchr/testify/require"
)

// These tests reproduce the concurrency hazards in Service: the stateMu RWMutex
// only guards the ticket *map*, not the *mm.Ticket aggregates stored in it. Every
// command follows GetTicket (RLock, then unlock) -> mutate ticket without a lock
// -> SaveTicket, so two goroutines touching the same ticket race on its fields
// (status, playerAcceptances, playerTeams, events).
//
// Run with the race detector to observe the failures:
//
//	go test -race -run TestRace ./internal/app/matchmaking/
//
// Several of them can also abort with a hard "fatal error: concurrent map
// writes" panic that takes down the whole process, since playerAcceptances and
// playerTeams are plain maps.

// blockingPublisher blocks the first Publish call until released, signalling
// when it has been entered. Later calls return immediately. It lets a test pin
// one event delivery in flight and observe whether the command lock is free
// while that delivery blocks.
type blockingPublisher struct {
	mu      sync.Mutex
	n       int
	entered chan struct{}
	release chan struct{}
}

func (b *blockingPublisher) Publish(_ context.Context, _ mm.Event) error {
	b.mu.Lock()
	b.n++
	first := b.n == 1
	b.mu.Unlock()
	if first {
		close(b.entered)
		<-b.release
	}
	return nil
}

// TestPublish_NotHeldUnderCommandLock verifies event publishing runs only after
// the per-configuration command lock is released (fix b). While one command is
// blocked delivering its event, another command for the same configuration must
// still make progress. If publishing held the command lock, the second command
// would block until the publisher returned and the test would time out.
func TestPublish_NotHeldUnderCommandLock(t *testing.T) {
	blocking := &blockingPublisher{entered: make(chan struct{}), release: make(chan struct{})}
	h := setupWithPublisher(t, skillRS, false, blocking)
	ctx := context.Background()

	// t1's MatchmakingSearching delivery blocks inside the publisher — but only
	// after StartMatchmaking has released the command lock.
	go func() {
		_, _ = h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1", TicketID: "t1",
			Players: []flexi.Player{{ID: "t1"}},
		})
	}()
	<-blocking.entered // a publish is now in flight; the command lock must be free

	// A second command on the same configuration must not wait for that publish.
	done := make(chan error, 1)
	go func() {
		_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1", TicketID: "t2",
			Players: []flexi.Player{{ID: "t2"}},
		})
		done <- err
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("second command blocked while a publish was in flight: publishing is holding the command lock")
	}
	close(blocking.release)
}

// proposeTwoTickets starts t1/t2 and ticks once so both sit in
// REQUIRES_ACCEPTANCE, the state AcceptMatch operates on.
func proposeTwoTickets(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	for _, id := range []mm.TicketID{"t1", "t2"} {
		_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1",
			TicketID:          id,
			Players:           []flexi.Player{{ID: string(id)}},
		})
		require.NoError(t, err)
	}
	require.NoError(t, h.svc.Tick(ctx, "c1"))
	tk, _ := h.svc.GetTicket("t1")
	require.Equal(t, mm.StatusRequiresAcceptance, tk.Status())
}

// Scenario 1: a player's AcceptMatch (API handler goroutine) lands at the exact
// moment the per-configuration Tick (Ticker goroutine) settles the same
// proposal's acceptance timeout. Tick's transitionFromEngine cancels the ticket
// — writing status/statusReason/events and reading every sibling ticket's
// PlayerAcceptances via acceptanceFailureOutcome — while AcceptMatch writes
// playerAcceptances on the same tickets. Each iteration advances the clock past
// the acceptance timeout so the Tick actually does the cancelling work that the
// late accept races against.
func TestRace_AcceptMatchVsTick(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		h := setup(t, skillRSAccept, true)
		proposeTwoTickets(t, h)
		// Push past the engine's acceptance timeout so the next Tick cancels the
		// un-accepted proposal.
		h.clock.Advance(31 * time.Second)

		var wg sync.WaitGroup
		start := make(chan struct{})

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = h.svc.AcceptMatch(ctx, appmm.AcceptMatchCommand{
				TicketID:  "t1",
				PlayerIDs: []mm.PlayerID{"t1"},
				Accepted:  true,
			})
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = h.svc.Tick(ctx, "c1")
		}()

		close(start)
		wg.Wait()
	}
}

// Scenario 2: two AcceptMatch calls land at the same time (the normal case when
// several players in a match accept simultaneously). Each goroutine writes its
// own ticket's playerAcceptances and then reads every sibling ticket's
// PlayerAcceptances() while building the cumulative AcceptMatch event, so they
// race on each other's maps. A hard "fatal error: concurrent map writes/read"
// can also abort the process here.
func TestRace_ConcurrentAcceptMatch(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		h := setup(t, skillRSAccept, true)
		proposeTwoTickets(t, h)

		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, id := range []mm.TicketID{"t1", "t2"} {
			wg.Add(1)
			go func(id mm.TicketID) {
				defer wg.Done()
				<-start
				_ = h.svc.AcceptMatch(ctx, appmm.AcceptMatchCommand{
					TicketID:  id,
					PlayerIDs: []mm.PlayerID{mm.PlayerID(id)},
					Accepted:  true,
				})
			}(id)
		}
		close(start)
		wg.Wait()
	}
}

// Scenario 3: DescribeMatchmaking (read path) races Tick (write path). Describe
// hands raw *mm.Ticket pointers to its caller, which reads Players /
// PlayerAcceptances / PlayerTeam while Tick concurrently calls SetPlayerTeams
// (writes the playerTeams map), flips status, and appends events.
func TestRace_DescribeVsTick(t *testing.T) {
	h := setup(t, skillRS, false)
	ctx := context.Background()
	for _, id := range []mm.TicketID{"t1", "t2"} {
		_, err := h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
			ConfigurationName: "c1",
			TicketID:          id,
			Players:           []flexi.Player{{ID: string(id)}},
		})
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			tickets, _ := h.svc.DescribeMatchmaking(ctx, appmm.DescribeMatchmakingQuery{
				TicketIDs: []mm.TicketID{"t1", "t2"},
			})
			for _, tk := range tickets {
				_ = tk.Status()
				_ = tk.Players()
				_ = tk.PlayerAcceptances()
				for _, p := range tk.Players() {
					_ = tk.PlayerTeam(mm.PlayerID(p.ID))
				}
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			_ = h.svc.Tick(ctx, "c1")
		}
	}()

	close(start)
	wg.Wait()
}

// Scenario 4: StartMatchmaking saves the ticket to the map and *then* drains its
// events (PullEvents). A Tick running in parallel picks the freshly-saved ticket
// up, flips it to SEARCHING and drains the same events slice, so the two
// goroutines race on t.events — the SearchingStarted event can be dropped or
// published twice.
func TestRace_StartVsTick(t *testing.T) {
	ctx := context.Background()
	for round := 0; round < 50; round++ {
		h := setup(t, skillRS, false)

		var wg sync.WaitGroup
		start := make(chan struct{})

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 50; i++ {
				_, _ = h.svc.StartMatchmaking(ctx, appmm.StartMatchmakingCommand{
					ConfigurationName: "c1",
					TicketID:          mm.TicketID(fmt.Sprintf("s-%d", i)),
					Players:           []flexi.Player{{ID: fmt.Sprintf("p-%d", i)}},
				})
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 200; i++ {
				_ = h.svc.Tick(ctx, "c1")
			}
		}()

		close(start)
		wg.Wait()
	}
}
