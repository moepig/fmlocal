package matchmaking

import mm "github.com/moepig/fmlocal/internal/domain/matchmaking"

type StartMatchmakingCommand struct {
	ConfigurationName mm.ConfigurationName
	TicketID          mm.TicketID
	Players           []mm.Player
}

// StartMatchBackfillCommand asks to fill the empty seats of a match already
// under way. Every player carries the Team they occupy in the running session.
// GameSessionARN is optional; when set it is the key an earlier, still-waiting
// backfill request for the same session is superseded by.
type StartMatchBackfillCommand struct {
	ConfigurationName mm.ConfigurationName
	TicketID          mm.TicketID
	GameSessionARN    string
	Players           []mm.Player
}

type StopMatchmakingCommand struct {
	TicketID mm.TicketID
}

type DescribeMatchmakingQuery struct {
	TicketIDs []mm.TicketID
}

type AcceptMatchCommand struct {
	TicketID  mm.TicketID
	PlayerIDs []mm.PlayerID
	Accepted  bool
}

type DescribeConfigurationsQuery struct {
	Names       []mm.ConfigurationName
	RuleSetName mm.RuleSetName
}

type DescribeRuleSetsQuery struct {
	Names []mm.RuleSetName
}

type ValidateRuleSetCommand struct {
	Body []byte
}
