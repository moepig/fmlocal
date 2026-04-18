package matchmaking

import "time"

// RuleSet is the parsed-once but-otherwise-opaque FlexMatch rule set. The
// Body is the verbatim JSON that an engine adapter feeds to flexi.
type RuleSet struct {
	Name         RuleSetName
	ARN          string
	Body         []byte
	CreationTime time.Time
}
