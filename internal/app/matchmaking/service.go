package matchmaking

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/moepig/fmlocal/internal/app/ports"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

type Service struct {
	Engines    EngineResolver
	Publishers map[mm.ConfigurationName]ports.EventPublisher
	Clock      ports.Clock
	IDs        ports.IDGenerator
	MatchIDs   ports.IDGenerator
	Logger     *slog.Logger

	stateMu        sync.RWMutex
	tickets        map[mm.TicketID]*mm.Ticket
	configurations map[mm.ConfigurationName]mm.Configuration
	ruleSets       map[mm.RuleSetName]mm.RuleSet

	trackersOnce sync.Once
	trackersMu   sync.Mutex
	trackers     map[mm.ConfigurationName]*proposalTracker
}

// LoadConfigurations installs the configurations fmlocal serves. It replaces
// any previously loaded set; intended for startup wiring.
func (s *Service) LoadConfigurations(cfgs []mm.Configuration) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.configurations = make(map[mm.ConfigurationName]mm.Configuration, len(cfgs))
	for _, c := range cfgs {
		s.configurations[c.Name] = c
	}
}

// LoadRuleSets installs the rule sets fmlocal serves. It replaces any
// previously loaded set; intended for startup wiring.
func (s *Service) LoadRuleSets(sets []mm.RuleSet) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.ruleSets = make(map[mm.RuleSetName]mm.RuleSet, len(sets))
	for _, rs := range sets {
		s.ruleSets[rs.Name] = rs
	}
}

// GetTicket returns the ticket with id, or mm.ErrTicketNotFound.
func (s *Service) GetTicket(id mm.TicketID) (*mm.Ticket, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	t, ok := s.tickets[id]
	if !ok {
		return nil, mm.ErrTicketNotFound
	}
	return t, nil
}

// GetTickets returns the tickets matching ids, preserving input order and
// silently skipping unknown ids.
func (s *Service) GetTickets(ids []mm.TicketID) []*mm.Ticket {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	out := make([]*mm.Ticket, 0, len(ids))
	for _, id := range ids {
		if t, ok := s.tickets[id]; ok {
			out = append(out, t)
		}
	}
	return out
}

// TicketsByConfiguration returns all tickets belonging to the given
// configuration, sorted by ticket id.
func (s *Service) TicketsByConfiguration(name mm.ConfigurationName) []*mm.Ticket {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	out := make([]*mm.Ticket, 0)
	for _, t := range s.tickets {
		if t.ConfigurationName() == name {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// ActiveTicketIDsByConfiguration returns the ids of still-active tickets
// belonging to the given configuration, sorted.
func (s *Service) ActiveTicketIDsByConfiguration(name mm.ConfigurationName) []mm.TicketID {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	out := make([]mm.TicketID, 0)
	for _, t := range s.tickets {
		if t.ConfigurationName() != name {
			continue
		}
		if t.Status().IsActive() {
			out = append(out, t.ID())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// GetConfiguration returns the configuration by name, or
// mm.ErrConfigurationNotFound.
func (s *Service) GetConfiguration(name mm.ConfigurationName) (mm.Configuration, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	c, ok := s.configurations[name]
	if !ok {
		return mm.Configuration{}, mm.ErrConfigurationNotFound
	}
	return c, nil
}

// ListConfigurations returns all configurations, sorted by name.
func (s *Service) ListConfigurations() []mm.Configuration {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	out := make([]mm.Configuration, 0, len(s.configurations))
	for _, c := range s.configurations {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GetRuleSet returns the rule set by name, or mm.ErrRuleSetNotFound.
func (s *Service) GetRuleSet(name mm.RuleSetName) (mm.RuleSet, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	rs, ok := s.ruleSets[name]
	if !ok {
		return mm.RuleSet{}, mm.ErrRuleSetNotFound
	}
	return rs, nil
}

// ListRuleSets returns all rule sets, sorted by name.
func (s *Service) ListRuleSets() []mm.RuleSet {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	out := make([]mm.RuleSet, 0, len(s.ruleSets))
	for _, rs := range s.ruleSets {
		out = append(out, rs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SaveTicket is the single write-path for the ticket map.
func (s *Service) SaveTicket(t *mm.Ticket) error {
	if t == nil {
		return fmt.Errorf("matchmaking: nil ticket")
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.tickets == nil {
		s.tickets = map[mm.TicketID]*mm.Ticket{}
	}
	s.tickets[t.ID()] = t
	return nil
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) publisher(name mm.ConfigurationName) ports.EventPublisher {
	if p, ok := s.Publishers[name]; ok && p != nil {
		return p
	}
	return noopPublisher{}
}

func (s *Service) dispatchEvents(ctx context.Context, name mm.ConfigurationName, t *mm.Ticket) {
	pub := s.publisher(name)
	for _, ev := range t.PullEvents() {
		if err := pub.Publish(ctx, ev); err != nil {
			s.logger().Warn("publish event failed",
				"configuration", name, "event", ev.EventName(), "error", err.Error())
		}
	}
}

type noopPublisher struct{}

func (noopPublisher) Publish(_ context.Context, _ mm.Event) error { return nil }
