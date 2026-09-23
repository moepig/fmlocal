package matchmaking

import (
	"context"
	"errors"
	"fmt"

	"github.com/moepig/flexi"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func (s *Service) StartMatchmaking(ctx context.Context, cmd StartMatchmakingCommand) (*mm.Ticket, error) {
	if len(cmd.Players) == 0 {
		return nil, fmt.Errorf("%w: players required", ErrInvalidCommand)
	}
	cfg, err := s.GetConfiguration(cmd.ConfigurationName)
	if err != nil {
		return nil, err
	}
	name := cfg.Name
	lane, err := s.reserveDelivery(ctx, name)
	if err != nil {
		return nil, err
	}
	unlock := s.lockConfiguration(name)
	batch := newEventBatch(s, name, lane)
	defer s.releaseAndFlush(ctx, unlock, batch)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg, err = s.GetConfiguration(name)
	if err != nil {
		return nil, err
	}
	engine, err := s.Engines.EngineFor(name)
	if err != nil {
		return nil, err
	}
	if err := s.validateRequiredAttributes(cfg, cmd.Players); err != nil {
		return nil, err
	}
	id := cmd.TicketID
	if id == "" {
		generated, err := mm.NewTicketID(s.IDs.NewID())
		if err != nil {
			return nil, err
		}
		id = generated
	}
	ticket, err := mm.NewTicket(id, cfg, cmd.Players, s.Clock.Now())
	if err != nil {
		return nil, err
	}
	if err := s.reserveTicketID(id); err != nil {
		return nil, err
	}
	if err := engine.Enqueue(flexi.Ticket{ID: string(id), Players: cmd.Players}); err != nil {
		s.cancelTicketReservation(id)
		switch {
		case errors.Is(err, flexi.ErrDuplicateTicket):
			return nil, mm.ErrTicketAlreadyExists
		case errors.Is(err, flexi.ErrInvalidTicket):
			// The ticket was rejected for its own contents, which is the
			// caller's mistake (AWS: 400 InvalidRequestException). Anything
			// else is an engine fault and stays a 500.
			return nil, fmt.Errorf("%w: engine enqueue: %v", mm.ErrInvalidRequest, err)
		}
		return nil, fmt.Errorf("engine enqueue: %w", err)
	}
	s.commitTicket(ticket)
	batch.addTicket(ticket)
	return ticket, nil
}
