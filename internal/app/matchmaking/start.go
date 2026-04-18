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
	engine, err := s.Engines.EngineFor(cmd.ConfigurationName)
	if err != nil {
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
	if err := engine.Enqueue(flexi.Ticket{ID: string(id), Players: cmd.Players}); err != nil {
		if errors.Is(err, flexi.ErrDuplicateTicket) {
			return nil, mm.ErrTicketAlreadyExists
		}
		return nil, fmt.Errorf("engine enqueue: %w", err)
	}
	if err := s.SaveTicket(ticket); err != nil {
		return nil, err
	}
	s.dispatchEvents(ctx, cfg.Name, ticket)
	return ticket, nil
}
