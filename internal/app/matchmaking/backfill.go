package matchmaking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moepig/flexi"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

// StartMatchBackfill queues a request to fill the empty seats of a match that
// is already under way, the standalone counterpart of AWS's StartMatchBackfill.
// The resulting ticket is an ordinary one: it searches, times out, is stopped
// with StopMatchmaking and completes through the same events as any other.
//
// When cmd.GameSessionARN is set the request opts into FlexMatch's rule that a
// game session has at most one outstanding backfill request, superseding an
// earlier one still waiting for a match. fmlocal performs that supersession
// itself rather than handing flexi the game session id, because it owns the
// ticket's lifecycle: cancelling the earlier ticket here emits
// MatchmakingCancelled with the right reason at the right moment, whereas a
// cancellation performed inside the engine would only surface on the next tick,
// indistinguishable from an acceptance failure.
func (s *Service) StartMatchBackfill(ctx context.Context, cmd StartMatchBackfillCommand) (*mm.Ticket, error) {
	if len(cmd.Players) == 0 {
		return nil, fmt.Errorf("%w: players required", ErrInvalidCommand)
	}
	unlock := s.lockConfiguration(cmd.ConfigurationName)
	batch := newEventBatch(cmd.ConfigurationName)
	defer s.releaseAndFlush(ctx, unlock, batch)
	cfg, err := s.GetConfiguration(cmd.ConfigurationName)
	if err != nil {
		return nil, err
	}
	engine, err := s.Engines.EngineFor(cmd.ConfigurationName)
	if err != nil {
		return nil, err
	}
	now := s.Clock.Now()

	// Locate the request this one replaces before anything is mutated, so a
	// session whose backfill is mid-proposal is refused without side effects.
	var superseded *mm.Ticket
	if cmd.GameSessionARN != "" {
		superseded = s.findSessionBackfill(cfg.Name, cmd.GameSessionARN)
		if superseded != nil && !isSupersedable(superseded.Status()) {
			return nil, mm.ErrBackfillInProgress
		}
	}

	id := cmd.TicketID
	if id == "" {
		generated, err := mm.NewTicketID(s.IDs.NewID())
		if err != nil {
			return nil, err
		}
		id = generated
	}
	ticket, err := mm.NewBackfillTicket(id, cfg, cmd.Players, cmd.GameSessionARN, now)
	if err != nil {
		return nil, err
	}
	// Enqueue before retiring the superseded ticket: a roster the engine
	// rejects must not have cost the caller the request it already had. No tick
	// can observe the two coexisting, since both run under the configuration
	// lock this command holds. GameSessionID is deliberately left empty — the
	// supersession below is fmlocal's job, not the engine's.
	if err := engine.EnqueueBackfill(flexi.Ticket{ID: string(id), Players: cmd.Players}); err != nil {
		switch {
		case errors.Is(err, flexi.ErrDuplicateTicket):
			return nil, mm.ErrTicketAlreadyExists
		case errors.Is(err, flexi.ErrInvalidTicket):
			// The roster was rejected for its own contents — a missing or
			// unknown Team, an attribute whose kind disagrees with the rule
			// set — which is the caller's mistake (AWS: 400
			// InvalidRequestException). Anything else is an engine fault and
			// stays a 500.
			return nil, fmt.Errorf("%w: engine enqueue backfill: %v", mm.ErrInvalidRequest, err)
		}
		return nil, fmt.Errorf("engine enqueue backfill: %w", err)
	}
	if superseded != nil {
		if err := s.supersedeBackfill(engine, superseded, now, batch); err != nil {
			return nil, err
		}
	}
	if err := s.SaveTicket(ticket); err != nil {
		return nil, err
	}
	batch.addTicket(ticket)
	return ticket, nil
}

// isSupersedable reports whether a backfill ticket in this state can still be
// replaced by a newer request for the same game session. A ticket that has
// already been matched — awaiting acceptance, or being placed — is not: tearing
// down a live proposal would cancel the sibling tickets in it and emit an
// acceptance outcome nobody asked for. AWS is assumed to replace it anyway;
// fmlocal deviates here deliberately (see README).
func isSupersedable(status mm.TicketStatus) bool {
	return status == mm.StatusQueued || status == mm.StatusSearching
}

// supersedeBackfill retires the earlier backfill request for a game session,
// emitting its MatchmakingCancelled.
func (s *Service) supersedeBackfill(engine *flexi.Matchmaker, prev *mm.Ticket, now time.Time, batch *eventBatch) error {
	captureRuleMetrics(engine, prev)
	if err := engine.Cancel(string(prev.ID())); err != nil && !errors.Is(err, flexi.ErrUnknownTicket) {
		return fmt.Errorf("engine cancel (superseded backfill): %w", err)
	}
	if err := prev.MarkCancelledBySuperseded(now); err != nil {
		return err
	}
	if err := s.SaveTicket(prev); err != nil {
		return err
	}
	batch.addTicket(prev)
	return nil
}

// findSessionBackfill returns the configuration's active backfill ticket for
// gameSessionARN, or nil when the session has none.
//
// The lookup scans the configuration's tickets rather than consulting a
// gameSessionARN index. It runs once per StartMatchBackfill over the same map
// every tick already walks, and an index would have to be swept on each of the
// several paths a ticket leaves the pool by — match, timeout, cancellation,
// acceptance failure — where a missed sweep would resurrect a dead ticket.
func (s *Service) findSessionBackfill(name mm.ConfigurationName, gameSessionARN string) *mm.Ticket {
	for _, t := range s.TicketsByConfiguration(name) {
		if t.IsBackfill() && t.GameSessionARN() == gameSessionARN && t.Status().IsActive() {
			return t
		}
	}
	return nil
}
