package matchmaking

import (
	"testing"

	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func TestProposalTracker_ForgetReleasesEntry(t *testing.T) {
	pt := newProposalTracker()
	roster := []mm.TicketID{"t2", "t1"}
	pt.assign(roster, "match-1")

	if id, ok := pt.known(roster); !ok || id != "match-1" {
		t.Fatalf("known after assign = (%q, %v), want (match-1, true)", id, ok)
	}
	if got := pt.ticketsFor("match-1"); len(got) != 2 {
		t.Fatalf("ticketsFor after assign = %v, want 2 ids", got)
	}

	pt.forget("match-1")

	if id, ok := pt.known(roster); ok {
		t.Fatalf("known after forget = (%q, %v), want (_, false)", id, ok)
	}
	if got := pt.ticketsFor("match-1"); got != nil {
		t.Fatalf("ticketsFor after forget = %v, want nil", got)
	}

	// Forgetting an unknown match is a no-op, not a panic.
	pt.forget("match-unknown")
}

// TestProposalTracker_ReassignAfterForget verifies the roster key is reusable
// after forget so a re-proposal of the same tickets gets a fresh match id
// rather than silently reusing the dead match's id.
func TestProposalTracker_ReassignAfterForget(t *testing.T) {
	pt := newProposalTracker()
	roster := []mm.TicketID{"t1", "t2"}

	pt.assign(roster, "match-1")
	pt.forget("match-1")

	if _, ok := pt.known(roster); ok {
		t.Fatalf("known after forget = true, want false so a new id is minted")
	}
	pt.assign(roster, "match-2")
	if id, ok := pt.known(roster); !ok || id != "match-2" {
		t.Fatalf("known after reassign = (%q, %v), want (match-2, true)", id, ok)
	}
}
