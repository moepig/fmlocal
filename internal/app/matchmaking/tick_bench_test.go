package matchmaking_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/moepig/flexi"
	"github.com/moepig/fmlocal/internal/app/defaults/sysclock"
	appmm "github.com/moepig/fmlocal/internal/app/matchmaking"
	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

func BenchmarkTickWithRetainedTickets(b *testing.B) {
	for _, count := range []int{0, 10_000, 100_000} {
		b.Run(fmt.Sprintf("retained=%d", count), func(b *testing.B) {
			clk := sysclock.NewFake(time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC))
			cfg := mm.Configuration{Name: "c1", RuleSetName: "rs1", FlexMatchMode: mm.FlexMatchModeStandalone}
			rs := mm.RuleSet{Name: "rs1", Body: []byte(skillRS)}
			engine, err := appmm.BuildEngine(cfg, rs, flexi.WithClock(clk))
			if err != nil {
				b.Fatal(err)
			}
			resolver := appmm.NewStaticEngineResolver()
			resolver.Register(cfg.Name, engine)
			svc := &appmm.Service{Engines: resolver, Clock: clk}
			svc.LoadConfigurations([]mm.Configuration{cfg})
			for i := range count {
				ticket, err := mm.NewTicket(mm.TicketID(fmt.Sprintf("ticket-%d", i)), cfg, []flexi.Player{{ID: fmt.Sprintf("player-%d", i)}}, clk.Now())
				if err != nil {
					b.Fatal(err)
				}
				if err := ticket.MarkTimedOut("TimedOut", "Matchmaking timed out", clk.Now()); err != nil {
					b.Fatal(err)
				}
				ticket.PullEvents()
				if err := svc.SaveTicket(ticket); err != nil {
					b.Fatal(err)
				}
			}
			if err := svc.Tick(context.Background(), cfg.Name); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for range b.N {
				if err := svc.Tick(context.Background(), cfg.Name); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if err := svc.CloseDelivery(context.Background()); err != nil {
				b.Fatal(err)
			}
		})
	}
}
