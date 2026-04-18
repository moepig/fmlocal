// Package ports declares the driven (outbound) interfaces that the
// application layer uses to interact with the world. Concrete implementations
// live in internal/infrastructure/*.
package ports

import "time"

type Clock interface {
	Now() time.Time
}
