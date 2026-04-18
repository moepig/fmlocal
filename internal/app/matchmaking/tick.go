package matchmaking

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moepig/flexi"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

type proposalTracker struct {
	mu       sync.Mutex
	matchIDs map[string]mm.MatchID
}

func newProposalTracker() *proposalTracker {
	return &proposalTracker{matchIDs: map[string]mm.MatchID{}}
}

func proposalKey(ids []mm.TicketID) string {
	cp := make([]string, len(ids))
	for i, id := range ids {
		cp[i] = string(id)
	}
	sort.Strings(cp)
	return strings.Join(cp, "|")
}

func (pt *proposalTracker) known(ids []mm.TicketID) (mm.MatchID, bool) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	m, ok := pt.matchIDs[proposalKey(ids)]
	return m, ok
}

func (pt *proposalTracker) assign(ids []mm.TicketID, id mm.MatchID) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.matchIDs[proposalKey(ids)] = id
}

func (s *Service) tracker(name mm.ConfigurationName) *proposalTracker {
	s.trackersOnce.Do(func() { s.trackers = map[mm.ConfigurationName]*proposalTracker{} })
	s.trackersMu.Lock()
	defer s.trackersMu.Unlock()
	pt, ok := s.trackers[name]
	if !ok {
		pt = newProposalTracker()
		s.trackers[name] = pt
	}
	return pt
}

func (s *Service) Tick(ctx context.Context, name mm.ConfigurationName) error {
	cfg, err := s.GetConfiguration(name)
	if err != nil {
		return err
	}
	engine, err := s.Engines.EngineFor(name)
	if err != nil {
		return err
	}
	now := s.Clock.Now()

	if err := s.enforceRequestTimeouts(ctx, cfg, engine, now); err != nil {
		return err
	}
	before := engine.PendingAcceptances()
	matches, err := engine.Tick()
	if err != nil {
		return fmt.Errorf("matchmaking: tick: %w", err)
	}
	after := engine.PendingAcceptances()
	if err := s.applyNewProposals(ctx, name, before, after, now); err != nil {
		return err
	}
	if err := s.finalizeMatches(ctx, cfg, engine, matches, now); err != nil {
		return err
	}
	return s.syncActiveTickets(ctx, cfg, engine, now)
}

func (s *Service) enforceRequestTimeouts(ctx context.Context, cfg mm.Configuration, engine *flexi.Matchmaker, now time.Time) error {
	if cfg.RequestTimeout <= 0 {
		return nil
	}
	ids := s.ActiveTicketIDsByConfiguration(cfg.Name)
	for _, id := range ids {
		ticket, err := s.GetTicket(id)
		if err != nil {
			continue
		}
		if ticket.Status() != mm.StatusQueued && ticket.Status() != mm.StatusSearching {
			continue
		}
		if now.Sub(ticket.StartTime()) < cfg.RequestTimeout {
			continue
		}
		if err := engine.Cancel(string(ticket.ID())); err != nil && !errors.Is(err, flexi.ErrUnknownTicket) {
			return fmt.Errorf("engine cancel (request timeout): %w", err)
		}
		if err := ticket.MarkTimedOut("TimedOut", "Matchmaking request timed out", now); err != nil {
			return err
		}
		if err := s.SaveTicket(ticket); err != nil {
			return err
		}
		s.dispatchEvents(ctx, cfg.Name, ticket)
	}
	return nil
}

func toTicketIDs(ss []string) []mm.TicketID {
	out := make([]mm.TicketID, len(ss))
	for i, s := range ss {
		out[i] = mm.TicketID(s)
	}
	return out
}

func (s *Service) applyNewProposals(ctx context.Context, name mm.ConfigurationName, before, after []flexi.Proposal, now time.Time) error {
	tracker := s.tracker(name)
	seen := map[string]bool{}
	for _, p := range before {
		seen[proposalKey(toTicketIDs(p.TicketIDs))] = true
	}
	for _, p := range after {
		tids := toTicketIDs(p.TicketIDs)
		key := proposalKey(tids)
		if seen[key] {
			continue
		}
		matchID, ok := tracker.known(tids)
		if !ok {
			matchID = mm.MatchID(s.MatchIDs.NewID())
			tracker.assign(tids, matchID)
		}
		for _, tid := range tids {
			ticket, err := s.GetTicket(tid)
			if err != nil || ticket.Status() == mm.StatusRequiresAcceptance {
				continue
			}
			if err := ticket.AssignToProposal(matchID, now); err != nil {
				return err
			}
			if err := s.SaveTicket(ticket); err != nil {
				return err
			}
			s.dispatchEvents(ctx, name, ticket)
		}
	}
	return nil
}

func (s *Service) finalizeMatches(ctx context.Context, cfg mm.Configuration, engine *flexi.Matchmaker, matches []flexi.Match, now time.Time) error {
	for _, m := range matches {
		tids := toTicketIDs(m.TicketIDs)
		for _, tid := range tids {
			ticket, err := s.GetTicket(tid)
			if err != nil {
				continue
			}
			if cfg.AcceptanceRequired && ticket.Status() == mm.StatusRequiresAcceptance {
				ticket.AcceptanceCompleted(mm.AcceptanceAccepted, now)
			}
			if ticket.Status() != mm.StatusPlacing {
				if err := ticket.MoveToPlacing(ticket.MatchID(), now); err != nil {
					return err
				}
			}
			if err := engine.MarkCompleted(string(ticket.ID())); err != nil && !errors.Is(err, flexi.ErrUnknownTicket) {
				return fmt.Errorf("engine mark completed: %w", err)
			}
			if err := ticket.Complete(now); err != nil {
				return err
			}
			if err := s.SaveTicket(ticket); err != nil {
				return err
			}
			s.dispatchEvents(ctx, cfg.Name, ticket)
		}
	}
	return nil
}

func (s *Service) syncActiveTickets(ctx context.Context, cfg mm.Configuration, engine *flexi.Matchmaker, now time.Time) error {
	ids := s.ActiveTicketIDsByConfiguration(cfg.Name)
	for _, id := range ids {
		ticket, err := s.GetTicket(id)
		if err != nil {
			continue
		}
		engineStatus, err := engine.Status(string(id))
		if err != nil {
			continue
		}
		curr := mm.TicketStatus(engineStatus)
		if curr == ticket.Status() {
			continue
		}
		if err := s.transitionFromEngine(cfg, ticket, curr, now); err != nil {
			return err
		}
		if err := s.SaveTicket(ticket); err != nil {
			return err
		}
		s.dispatchEvents(ctx, cfg.Name, ticket)
	}
	return nil
}

func (s *Service) transitionFromEngine(cfg mm.Configuration, ticket *mm.Ticket, curr mm.TicketStatus, now time.Time) error {
	prev := ticket.Status()
	switch curr {
	case mm.StatusQueued, mm.StatusSearching:
		ticket.ObserveSearching()
	case mm.StatusCancelled:
		if prev == mm.StatusRequiresAcceptance {
			ticket.AcceptanceCompleted(mm.AcceptanceRejected, now)
		}
		if ticket.CancelRequestedByAPI() {
			return ticket.MarkCancelledByAPI(now)
		}
		return ticket.MarkCancelledViaReject(now)
	case mm.StatusTimedOut:
		if prev == mm.StatusRequiresAcceptance {
			ticket.AcceptanceCompleted(mm.AcceptanceTimedOut, now)
			return ticket.MarkTimedOut("TimedOut", "Proposal was not accepted within the timeout", now)
		}
		return ticket.MarkTimedOut("TimedOut", "Matchmaking timed out", now)
	}
	return nil
}
