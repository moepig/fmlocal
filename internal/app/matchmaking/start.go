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
		// Every other Enqueue error in flexi v0.2.0 is input validation (empty
		// id, no players, attribute kind mismatch), so it maps to the client
		// (AWS: 400 InvalidRequestException). Switch to errors.Is once flexi
		// exposes a dedicated sentinel for invalid tickets.
		return nil, fmt.Errorf("%w: engine enqueue: %v", mm.ErrInvalidRequest, err)
	}
	if err := s.SaveTicket(ticket); err != nil {
		return nil, err
	}
	batch.addTicket(ticket)
	return ticket, nil
}
