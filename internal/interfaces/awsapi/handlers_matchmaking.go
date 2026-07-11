package awsapi

import (
	"net/http"

	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

// maxStartMatchmakingPlayers is AWS's per-request player cap for
// StartMatchmaking.
const maxStartMatchmakingPlayers = 10

func (s *Server) handleStartMatchmaking(r *http.Request, body []byte) (any, error) {
	var in StartMatchmakingInput
	if err := s.decodeJSON(body, &in); err != nil {
		return nil, err
	}
	if len(in.Players) == 0 {
		return nil, newInvalidRequest("Players is required")
	}
	// AWS caps StartMatchmaking at 10 players per request (the Players member
	// has "Array Members: Minimum number of 1 item. Maximum number of 10").
	if len(in.Players) > maxStartMatchmakingPlayers {
		return nil, newInvalidRequest("Players must contain at most %d items, got %d", maxStartMatchmakingPlayers, len(in.Players))
	}
	// AWS rejects Team on regular matchmaking requests: "Do not specify a team
	// if you are not using backfill" (Player.Team documentation).
	for _, p := range in.Players {
		if p.Team != "" {
			return nil, newInvalidRequest("Player %q: Team must not be specified for StartMatchmaking", p.PlayerID)
		}
	}
	ticket, err := s.service.StartMatchmaking(r.Context(), appmm.StartMatchmakingCommand{
		ConfigurationName: mm.ConfigurationName(in.ConfigurationName),
		TicketID:          mm.TicketID(in.TicketID),
		Players:           playersFromDTO(in.Players),
	})
	if err != nil {
		return nil, err
	}
	return StartMatchmakingOutput{MatchmakingTicket: ticketToDTO(ticket)}, nil
}

func (s *Server) handleStopMatchmaking(r *http.Request, body []byte) (any, error) {
	var in StopMatchmakingInput
	if err := s.decodeJSON(body, &in); err != nil {
		return nil, err
	}
	if in.TicketID == "" {
		return nil, newInvalidRequest("TicketId is required")
	}
	if err := s.service.StopMatchmaking(r.Context(), appmm.StopMatchmakingCommand{
		TicketID: mm.TicketID(in.TicketID),
	}); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

func (s *Server) handleDescribeMatchmaking(r *http.Request, body []byte) (any, error) {
	var in DescribeMatchmakingInput
	if err := s.decodeJSON(body, &in); err != nil {
		return nil, err
	}
	if len(in.TicketIDs) == 0 {
		return nil, newInvalidRequest("TicketIds is required")
	}
	tickets, err := s.service.DescribeMatchmaking(r.Context(), appmm.DescribeMatchmakingQuery{TicketIDs: mm.ToTyped[mm.TicketID](in.TicketIDs)})
	if err != nil {
		return nil, err
	}
	out := DescribeMatchmakingOutput{TicketList: make([]MatchmakingTicket, 0, len(tickets))}
	for _, t := range tickets {
		out.TicketList = append(out.TicketList, ticketToDTO(t))
	}
	return out, nil
}

func (s *Server) handleAcceptMatch(r *http.Request, body []byte) (any, error) {
	var in AcceptMatchInput
	if err := s.decodeJSON(body, &in); err != nil {
		return nil, err
	}
	if in.TicketID == "" {
		return nil, newInvalidRequest("TicketId is required")
	}
	if in.AcceptanceType != "ACCEPT" && in.AcceptanceType != "REJECT" {
		return nil, newInvalidRequest("AcceptanceType must be ACCEPT or REJECT")
	}
	if len(in.PlayerIDs) == 0 {
		return nil, newInvalidRequest("PlayerIds is required")
	}
	if err := s.service.AcceptMatch(r.Context(), appmm.AcceptMatchCommand{
		TicketID:  mm.TicketID(in.TicketID),
		PlayerIDs: mm.ToTyped[mm.PlayerID](in.PlayerIDs),
		Accepted:  in.AcceptanceType == "ACCEPT",
	}); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

func (s *Server) handleStartMatchBackfill(_ *http.Request, _ []byte) (any, error) {
	return nil, newUnsupported("StartMatchBackfill is not supported by fmlocal")
}
