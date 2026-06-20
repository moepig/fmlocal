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
	byMatch  map[mm.MatchID][]mm.TicketID
}

func newProposalTracker() *proposalTracker {
	return &proposalTracker{matchIDs: map[string]mm.MatchID{}, byMatch: map[mm.MatchID][]mm.TicketID{}}
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
	cp := make([]mm.TicketID, len(ids))
	copy(cp, ids)
	pt.byMatch[id] = cp
}

// ticketsFor returns the full ticket roster recorded for a match, or nil if the
// match is unknown to the tracker.
func (pt *proposalTracker) ticketsFor(id mm.MatchID) []mm.TicketID {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.byMatch[id]
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
	defer s.lockConfiguration(name)()
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
	if err := s.applyNewProposals(ctx, cfg, before, after, now); err != nil {
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
		captureRuleMetrics(engine, ticket)
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

// playerTeams inverts the engine's team->players map into player->team so it
// can be recorded on individual tickets.
func playerTeams(teams map[string][]flexi.Player) map[string]string {
	out := make(map[string]string)
	for team, players := range teams {
		for _, p := range players {
			out[p.ID] = team
		}
	}
	return out
}

// toRuleMetrics converts the engine's rule-evaluation tallies into the domain
// type used by matchmaking events.
func toRuleMetrics(src []flexi.RuleMetric) []mm.RuleEvaluationMetric {
	if len(src) == 0 {
		return nil
	}
	out := make([]mm.RuleEvaluationMetric, len(src))
	for i, m := range src {
		out[i] = mm.RuleEvaluationMetric{RuleName: m.RuleName, PassedCount: m.PassedCount, FailedCount: m.FailedCount}
	}
	return out
}

// captureRuleMetrics records the engine's cumulative rule metrics for a ticket
// so its terminal (timed-out/cancelled) event can surface them.
func captureRuleMetrics(engine *flexi.Matchmaker, ticket *mm.Ticket) {
	if m, ok := engine.RuleMetrics(string(ticket.ID())); ok {
		ticket.SetRuleMetrics(toRuleMetrics(m))
	}
}

func (s *Service) applyNewProposals(ctx context.Context, cfg mm.Configuration, before, after []flexi.Proposal, now time.Time) error {
	name := cfg.Name
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
		teams := playerTeams(p.Teams)
		for _, tid := range tids {
			ticket, err := s.GetTicket(tid)
			if err != nil || ticket.Status() == mm.StatusRequiresAcceptance {
				continue
			}
			if err := ticket.AssignToProposal(matchID, now); err != nil {
				return err
			}
			ticket.SetPlayerTeams(teams)
			if err := s.SaveTicket(ticket); err != nil {
				return err
			}
		}
		// One PotentialMatchCreated per match, carrying every ticket, the
		// search's rule-evaluation metrics, and the config's acceptance policy.
		s.publishEvent(ctx, name, mm.NewPotentialMatchCreated(name, matchID, tids, toRuleMetrics(p.RuleEvaluationMetrics), cfg.AcceptanceRequired, cfg.AcceptanceTimeout, now))
	}
	return nil
}

func (s *Service) finalizeMatches(ctx context.Context, cfg mm.Configuration, engine *flexi.Matchmaker, matches []flexi.Match, now time.Time) error {
	tracker := s.tracker(cfg.Name)
	for _, m := range matches {
		tids := toTicketIDs(m.TicketIDs)
		// A match formed via acceptance already has a matchID from
		// applyNewProposals; a direct (no-acceptance) match gets one here so
		// the success event carries a stable id.
		matchID, ok := tracker.known(tids)
		directMatch := !ok
		if !ok {
			matchID = mm.MatchID(s.MatchIDs.NewID())
			tracker.assign(tids, matchID)
		}
		teams := playerTeams(m.Teams)
		acceptanceSettled := false
		for _, tid := range tids {
			ticket, err := s.GetTicket(tid)
			if err != nil {
				continue
			}
			ticket.SetPlayerTeams(teams)
			if cfg.AcceptanceRequired && ticket.Status() == mm.StatusRequiresAcceptance {
				acceptanceSettled = true
			}
			if ticket.Status() != mm.StatusPlacing {
				if err := ticket.MoveToPlacing(matchID, now); err != nil {
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
		}
		// A direct (no-acceptance) match never passed through applyNewProposals,
		// so emit its PotentialMatchCreated here. AWS emits this event for all
		// new potential matches regardless of whether acceptance is required.
		if directMatch {
			s.publishEvent(ctx, cfg.Name, mm.NewPotentialMatchCreated(cfg.Name, matchID, tids, toRuleMetrics(m.RuleEvaluationMetrics), cfg.AcceptanceRequired, cfg.AcceptanceTimeout, now))
		}
		// One AcceptMatchCompleted (if acceptance was required) and one
		// MatchmakingSucceeded per match, each carrying every ticket.
		if acceptanceSettled {
			s.publishEvent(ctx, cfg.Name, mm.NewAcceptMatchCompleted(cfg.Name, matchID, tids, mm.AcceptanceAccepted, now))
		}
		s.publishEvent(ctx, cfg.Name, mm.NewMatchmakingSucceeded(cfg.Name, matchID, tids, now))
	}
	return nil
}

func (s *Service) syncActiveTickets(ctx context.Context, cfg mm.Configuration, engine *flexi.Matchmaker, now time.Time) error {
	tracker := s.tracker(cfg.Name)
	// AcceptMatchCompleted is a match-level event: emit it at most once per
	// match even though each ticket settles independently in this loop.
	settled := map[mm.MatchID]bool{}
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
		matchID := ticket.MatchID()
		if curr == mm.StatusCancelled || curr == mm.StatusTimedOut {
			captureRuleMetrics(engine, ticket)
		}
		outcome, err := s.transitionFromEngine(cfg, engine, ticket, curr, now)
		if err != nil {
			return err
		}
		if err := s.SaveTicket(ticket); err != nil {
			return err
		}
		if outcome != "" && matchID != "" && !settled[matchID] {
			settled[matchID] = true
			tids := tracker.ticketsFor(matchID)
			if tids == nil {
				tids = []mm.TicketID{ticket.ID()}
			}
			s.publishEvent(ctx, cfg.Name, mm.NewAcceptMatchCompleted(cfg.Name, matchID, tids, outcome, now))
		}
		s.dispatchEvents(ctx, cfg.Name, ticket)
	}
	return nil
}

// transitionFromEngine applies the ticket's engine-driven status change and
// reports the acceptance outcome when a proposal terminally settled (so the
// caller can emit a single match-level AcceptMatchCompleted). The returned
// outcome is empty when the transition is not an acceptance settlement.
func (s *Service) transitionFromEngine(cfg mm.Configuration, engine *flexi.Matchmaker, ticket *mm.Ticket, curr mm.TicketStatus, now time.Time) (mm.AcceptanceOutcome, error) {
	prev := ticket.Status()
	switch curr {
	case mm.StatusQueued, mm.StatusSearching:
		// A ticket whose players all accepted a proposal that then failed
		// acceptance (a sibling rejected or timed out) is returned to the pool
		// by the engine; AWS re-emits MatchmakingSearching for it and reports a
		// status reason (ACCEPTANCE_FAILED) on the re-queued ticket.
		if prev == mm.StatusRequiresAcceptance {
			reason := ""
			if r, ok := engine.StatusReason(string(ticket.ID())); ok {
				reason = string(r)
			}
			return "", ticket.ReturnToSearching(reason, now)
		}
		ticket.ObserveSearching()
	case mm.StatusCancelled:
		// A user-initiated StopMatchmaking always wins.
		if ticket.CancelRequestedByAPI() {
			return "", ticket.MarkCancelledByAPI(now)
		}
		// Otherwise this is the ticket that caused a proposal's acceptance to
		// fail. AWS cancels it (CANCELLED, not FAILED) and reports the
		// match-level outcome — Rejected vs TimedOut — on AcceptMatchCompleted.
		if prev == mm.StatusRequiresAcceptance {
			return s.acceptanceFailureOutcome(cfg.Name, ticket.MatchID()), ticket.MarkCancelledByAcceptanceFailure(now)
		}
		return "", ticket.MarkCancelledByAcceptanceFailure(now)
	case mm.StatusTimedOut:
		// Only the request-level timeout reaches the engine as TIMED_OUT;
		// acceptance failures terminate as CANCELLED above.
		return "", ticket.MarkTimedOut("TimedOut", "Matchmaking timed out", now)
	}
	return "", nil
}

// acceptanceFailureOutcome classifies why a proposal's acceptance failed by
// inspecting the per-player decisions recorded across the match's tickets: an
// explicit rejection yields Rejected; otherwise the proposal lapsed and the
// outcome is TimedOut. The two map to AWS's AcceptMatchCompleted "acceptance".
func (s *Service) acceptanceFailureOutcome(name mm.ConfigurationName, matchID mm.MatchID) mm.AcceptanceOutcome {
	for _, tid := range s.tracker(name).ticketsFor(matchID) {
		ticket, err := s.GetTicket(tid)
		if err != nil {
			continue
		}
		for _, accepted := range ticket.PlayerAcceptances() {
			if !accepted {
				return mm.AcceptanceRejected
			}
		}
	}
	return mm.AcceptanceTimedOut
}
