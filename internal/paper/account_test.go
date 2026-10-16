package paper

import (
	"testing"
	"time"

	"tg-recs-audit/internal/contract"
)

type fakeStrat struct{}

func (fakeStrat) Name() string                                 { return "x" }
func (fakeStrat) Title() string                                { return "x" }
func (fakeStrat) Benchmark() string                            { return "B" }
func (fakeStrat) Capital() float64                             { return 1000 }
func (fakeStrat) Start() time.Time                             { return day("2026-10-12") }
func (fakeStrat) Ops(time.Time, Memory) ([]contract.Op, error) { return nil, nil }
func (fakeStrat) Mark(string, time.Time) (float64, bool)       { return 0, false }

func TestОперацияБезБараБенчмаркаВсёРавноВСчёте(t *testing.T) {
	bench := []contract.Bar{{Day: day("2026-10-14"), Close: 100}, {Day: day("2026-10-15"), Close: 101}}
	ops := []contract.Op{{Day: "2026-10-16", Kind: "dividend", Amount: 50}} // бара 16-го ещё нет
	c := Curve(fakeStrat{}, ops, bench, day("2026-10-17"))
	if last := c[len(c)-1]; last.Equity() != 1050 {
		t.Fatalf("%+v", c)
	}
}
