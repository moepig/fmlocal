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
	engine, err := s.Engines.EngineFor(ticket.ConfigurationName())
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
	s.dispatchEvents(ctx, ticket.ConfigurationName(), ticket)
	return nil
}
