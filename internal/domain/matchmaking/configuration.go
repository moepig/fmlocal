package matchmaking

import "time"

// Configuration is the settings entity for a matchmaking configuration. It
// controls the runtime policy that wraps a RuleSet — how long to wait, what
// publishers get its events, etc.
type Configuration struct {
	Name                     ConfigurationName
	ARN                      string
	RuleSetName              RuleSetName
	RuleSetARN               string
	RequestTimeout           time.Duration
	AcceptanceRequired       bool
	AcceptanceTimeout        time.Duration
	BackfillMode             BackfillMode
	FlexMatchMode            FlexMatchMode
	NotificationTargetIDs    []string
	CreationTime             time.Time
}

// FlexMatchMode selects whether the engine also places a GameSession after a
// match forms. fmlocal only supports STANDALONE.
type FlexMatchMode string

const (
	FlexMatchModeStandalone FlexMatchMode = "STANDALONE"
	FlexMatchModeWithQueue  FlexMatchMode = "WITH_QUEUE"
)

// BackfillMode mirrors the AWS GameLift BackfillMode enum (MANUAL / AUTOMATIC).
type BackfillMode string

const (
	BackfillManual    BackfillMode = "MANUAL"
	BackfillAutomatic BackfillMode = "AUTOMATIC"
)
