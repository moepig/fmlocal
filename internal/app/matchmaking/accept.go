package matchmaking

import (
	"context"
	"errors"

	"github.com/moepig/flexi"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func (s *Service) AcceptMatch(ctx context.Context, cmd AcceptMatchCommand) error {
	ticket, err := s.GetTicket(cmd.TicketID)
	if err != nil {
		return err
	}
	// ConfigurationName is fixed at construction, so reading it before taking the
	// lock is race-free; everything that mutates the ticket runs under the lock.
	name := ticket.ConfigurationName()
	unlock := s.lockConfiguration(name)
	batch := newEventBatch(name)
	defer s.releaseAndFlush(ctx, unlock, batch)
	engine, err := s.Engines.EngineFor(name)
	if err != nil {
		return err
	}
	for _, pid := range cmd.PlayerIDs {
		if err := ticket.RecordPlayerAcceptance(pid, cmd.Accepted, s.Clock.Now()); err != nil {
			return err
		}
		var engineErr error
		if cmd.Accepted {
			engineErr = engine.Accept(string(ticket.ID()), string(pid))
		} else {
			engineErr = engine.Reject(string(ticket.ID()), string(pid))
		}
		if engineErr != nil {
			switch {
			case errors.Is(engineErr, flexi.ErrUnknownTicket):
				return mm.ErrTicketNotFound
			case errors.Is(engineErr, flexi.ErrUnknownProposal):
				return mm.ErrProposalNotFound
			case errors.Is(engineErr, flexi.ErrUnknownPlayer):
				return mm.ErrPlayerNotInTicket
			default:
				return engineErr
			}
		}
	}
	if err := s.SaveTicket(ticket); err != nil {
		return err
	}
	// AcceptMatch is a match-level event: emit one notification covering every
	// ticket in the match, carrying the cumulative acceptance status of every
	// player who has responded so far (AWS reports each player's current state
	// on every AcceptMatch event, not just the ones who just responded).
	matchID := ticket.MatchID()
	tids := s.tracker(name).ticketsFor(matchID)
	if tids == nil {
		tids = []mm.TicketID{ticket.ID()}
	}
	acceptances := map[mm.PlayerID]bool{}
	for _, tid := range tids {
		tk, err := s.GetTicket(tid)
		if err != nil {
			continue
		}
		for pid, accepted := range tk.PlayerAcceptances() {
			acceptances[pid] = accepted
		}
	}
	batch.add(mm.NewAcceptMatch(name, matchID, tids, acceptances, s.Clock.Now()))
	return nil
}
