package matchmaking

import "github.com/moepig/flexi"

// Player, Attribute, Attributes, AttributeKind are type aliases to flexi's
// equivalents. fmlocal uses flexi as its matchmaking engine throughout; the
// domain-level Player is the same type — no translation needed.
type (
	Player     = flexi.Player
	Attribute  = flexi.Attribute
	Attributes = flexi.Attributes
)

type AttributeKind = flexi.AttributeKind

const (
	AttrUnknown         = flexi.AttrUnknown
	AttrString          = flexi.AttrString
	AttrNumber          = flexi.AttrNumber
	AttrStringList      = flexi.AttrStringList
	AttrStringNumberMap = flexi.AttrStringNumberMap
)
