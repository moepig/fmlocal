package awsapi

import (
	"net/http"

	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func (s *Server) handleStartMatchmaking(r *http.Request, body []byte) (any, error) {
	var in StartMatchmakingInput
	if err := decodeJSON(body, &in); err != nil {
		return nil, err
	}
	if len(in.Players) == 0 {
		return nil, newInvalidRequest("Players is required")
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
	if err := decodeJSON(body, &in); err != nil {
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
	if err := decodeJSON(body, &in); err != nil {
		return nil, err
	}
	if len(in.TicketIDs) == 0 {
		return nil, newInvalidRequest("TicketIds is required")
	}
	ids := make([]mm.TicketID, len(in.TicketIDs))
	for i, id := range in.TicketIDs {
		ids[i] = mm.TicketID(id)
	}
	tickets, err := s.service.DescribeMatchmaking(r.Context(), appmm.DescribeMatchmakingQuery{TicketIDs: ids})
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
	if err := decodeJSON(body, &in); err != nil {
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
	players := make([]mm.PlayerID, len(in.PlayerIDs))
	for i, id := range in.PlayerIDs {
		players[i] = mm.PlayerID(id)
	}
	if err := s.service.AcceptMatch(r.Context(), appmm.AcceptMatchCommand{
		TicketID:  mm.TicketID(in.TicketID),
		PlayerIDs: players,
		Accepted:  in.AcceptanceType == "ACCEPT",
	}); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

func (s *Server) handleStartMatchBackfill(_ *http.Request, _ []byte) (any, error) {
	return nil, newUnsupported("StartMatchBackfill is not supported by fmlocal")
}

func (s *Server) handleStopMatchBackfill(_ *http.Request, _ []byte) (any, error) {
	return nil, newUnsupported("StopMatchBackfill is not supported by fmlocal")
}
