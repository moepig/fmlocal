package matchmaking

import mm "github.com/moepig/fmlocal/internal/domain/matchmaking"

type StartMatchmakingCommand struct {
	ConfigurationName mm.ConfigurationName
	TicketID          mm.TicketID
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
