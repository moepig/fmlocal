package awsapi

// AWS wire DTOs. Field names and casing match what the AWS SDK sends/expects
// on the wire for the GameLift JSON 1.1 protocol.

type AttributeValue struct {
	S   *string            `json:"S,omitempty"`
	N   *float64           `json:"N,omitempty"`
	SL  []string           `json:"SL,omitempty"`
	SDM map[string]float64 `json:"SDM,omitempty"`
}

type Player struct {
	PlayerID         string                    `json:"PlayerId,omitempty"`
	Team             string                    `json:"Team,omitempty"`
	PlayerAttributes map[string]AttributeValue `json:"PlayerAttributes,omitempty"`
	LatencyInMs      map[string]int            `json:"LatencyInMs,omitempty"`
}

type MatchmakingTicket struct {
	TicketID          string   `json:"TicketId"`
	ConfigurationName string   `json:"ConfigurationName"`
	ConfigurationARN  string   `json:"ConfigurationArn"`
	Status            string   `json:"Status"`
	StatusReason      string   `json:"StatusReason,omitempty"`
	StatusMessage     string   `json:"StatusMessage,omitempty"`
	StartTime         float64  `json:"StartTime,omitempty"`
	EndTime           float64  `json:"EndTime,omitempty"`
	Players           []Player `json:"Players"`
	EstimatedWaitTime *int     `json:"EstimatedWaitTime,omitempty"`
}

type StartMatchmakingInput struct {
	ConfigurationName string   `json:"ConfigurationName"`
	TicketID          string   `json:"TicketId,omitempty"`
	Players           []Player `json:"Players"`
}

type StartMatchmakingOutput struct {
	MatchmakingTicket MatchmakingTicket `json:"MatchmakingTicket"`
}

type StopMatchmakingInput struct {
	TicketID string `json:"TicketId"`
}

type DescribeMatchmakingInput struct {
	TicketIDs []string `json:"TicketIds"`
}

type DescribeMatchmakingOutput struct {
	TicketList []MatchmakingTicket `json:"TicketList"`
}

type AcceptMatchInput struct {
	TicketID       string   `json:"TicketId"`
	PlayerIDs      []string `json:"PlayerIds"`
	AcceptanceType string   `json:"AcceptanceType"`
}

type MatchmakingConfiguration struct {
	Name                     string  `json:"Name"`
	ConfigurationARN         string  `json:"ConfigurationArn"`
	Description              string  `json:"Description,omitempty"`
	RuleSetName              string  `json:"RuleSetName"`
	RuleSetARN               string  `json:"RuleSetArn"`
	RequestTimeoutSeconds    int     `json:"RequestTimeoutSeconds"`
	AcceptanceRequired       bool    `json:"AcceptanceRequired"`
	AcceptanceTimeoutSeconds int     `json:"AcceptanceTimeoutSeconds,omitempty"`
	BackfillMode             string  `json:"BackfillMode,omitempty"`
	FlexMatchMode            string  `json:"FlexMatchMode"`
	NotificationTarget       string  `json:"NotificationTarget,omitempty"`
	CreationTime             float64 `json:"CreationTime,omitempty"`
}

type DescribeMatchmakingConfigurationsInput struct {
	Names       []string `json:"Names,omitempty"`
	RuleSetName string   `json:"RuleSetName,omitempty"`
	Limit       int      `json:"Limit,omitempty"`
	NextToken   string   `json:"NextToken,omitempty"`
}

type DescribeMatchmakingConfigurationsOutput struct {
	Configurations []MatchmakingConfiguration `json:"Configurations"`
	NextToken      string                     `json:"NextToken,omitempty"`
}

type MatchmakingRuleSet struct {
	RuleSetName  string  `json:"RuleSetName"`
	RuleSetARN   string  `json:"RuleSetArn"`
	RuleSetBody  string  `json:"RuleSetBody"`
	CreationTime float64 `json:"CreationTime,omitempty"`
}

type DescribeMatchmakingRuleSetsInput struct {
	Names     []string `json:"Names,omitempty"`
	Limit     int      `json:"Limit,omitempty"`
	NextToken string   `json:"NextToken,omitempty"`
}

type DescribeMatchmakingRuleSetsOutput struct {
	RuleSets  []MatchmakingRuleSet `json:"RuleSets"`
	NextToken string               `json:"NextToken,omitempty"`
}

type ValidateMatchmakingRuleSetInput struct {
	RuleSetBody string `json:"RuleSetBody"`
}

type ValidateMatchmakingRuleSetOutput struct {
	Valid bool `json:"Valid"`
}
