package awsapi

import (
	"net/http"

	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func (s *Server) handleDescribeConfigurations(r *http.Request, body []byte) (any, error) {
	var in DescribeMatchmakingConfigurationsInput
	if err := decodeJSON(body, &in); err != nil {
		return nil, err
	}
	names := make([]mm.ConfigurationName, len(in.Names))
	for i, n := range in.Names {
		names[i] = mm.ConfigurationName(n)
	}
	cfgs, err := s.service.DescribeConfigurations(r.Context(), appmm.DescribeConfigurationsQuery{
		Names:       names,
		RuleSetName: mm.RuleSetName(in.RuleSetName),
	})
	if err != nil {
		return nil, err
	}
	out := DescribeMatchmakingConfigurationsOutput{Configurations: make([]MatchmakingConfiguration, 0, len(cfgs))}
	for _, c := range cfgs {
		out.Configurations = append(out.Configurations, configurationToDTO(c))
	}
	return out, nil
}

func (s *Server) handleDescribeRuleSets(r *http.Request, body []byte) (any, error) {
	var in DescribeMatchmakingRuleSetsInput
	if err := decodeJSON(body, &in); err != nil {
		return nil, err
	}
	names := make([]mm.RuleSetName, len(in.Names))
	for i, n := range in.Names {
		names[i] = mm.RuleSetName(n)
	}
	rs, err := s.service.DescribeRuleSets(r.Context(), appmm.DescribeRuleSetsQuery{Names: names})
	if err != nil {
		return nil, err
	}
	out := DescribeMatchmakingRuleSetsOutput{RuleSets: make([]MatchmakingRuleSet, 0, len(rs))}
	for _, x := range rs {
		out.RuleSets = append(out.RuleSets, ruleSetToDTO(x))
	}
	return out, nil
}

func (s *Server) handleValidateRuleSet(r *http.Request, body []byte) (any, error) {
	var in ValidateMatchmakingRuleSetInput
	if err := decodeJSON(body, &in); err != nil {
		return nil, err
	}
	if err := s.service.ValidateRuleSet(r.Context(), appmm.ValidateRuleSetCommand{Body: []byte(in.RuleSetBody)}); err != nil {
		return nil, err
	}
	return ValidateMatchmakingRuleSetOutput{Valid: true}, nil
}
