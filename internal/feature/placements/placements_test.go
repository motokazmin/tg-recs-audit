package placements

import (
	"math"
	"testing"
	"time"

	"tg-recs-audit/internal/contract"
)

type fake struct {
	bars  []contract.BondBar
	flows []contract.BondFlow
}

func (f fake) Card(string) (map[string]string, error)                 { return nil, nil }
func (f fake) BondHead(string, time.Time) ([]contract.BondBar, error) { return f.bars, nil }
func (f fake) BondFlows(string) ([]contract.BondFlow, error)          { return f.flows, nil }

func day(i int) time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i) }

// LQDT растёт на 0.05% в день, календарь — каждый день.
func lqdt(n int) []contract.Bar {
	var out []contract.Bar
	for i := 0; i < n; i++ {
		out = append(out, contract.Bar{Day: day(i), Close: math.Pow(1.0005, float64(i))})
	}
	return out
}

func TestДоходРазмещенияСКупономИКомиссией(t *testing.T) {
	m := fake{
		bars:  []contract.BondBar{{Day: day(0), Close: 100, Face: 1000}, {Day: day(5), Close: 101, AccInt: 3, Face: 1000}},
		flows: []contract.BondFlow{{Day: day(3), ValueRub: 15}, {Day: day(9), ValueRub: 15}}, // второй купон — за окном
	}
	obs, err := Evaluate(Bond{SecID: "B", Issue: day(0), Coupon: 20}, m, lqdt(10), []int{5})
	if err != nil || len(obs) != 1 {
		t.Fatal(err, obs)
	}
	want := (1010+3+15)*(1-exitCommission)/1000 - 1
	if o := obs[0]; math.Abs(o.Ret-want) > 1e-12 || math.Abs(o.MM-(math.Pow(1.0005, 5)-1)) > 1e-12 {
		t.Fatalf("%+v, want ret %v", o, want)
	}
}

func TestБезСделокПослеРазмещенияОтдельно(t *testing.T) {
	m := fake{bars: []contract.BondBar{{Day: day(0), Close: 100, Face: 1000}}} // только день размещения
	obs, _ := Evaluate(Bond{SecID: "B", Issue: day(0)}, m, lqdt(10), []int{5})
	if len(obs) != 1 || !obs[0].NoTrades {
		t.Fatalf("сделка в день размещения — это вход, не выход: %+v", obs)
	}
	if s := Summarize(obs); s.N != 0 {
		t.Fatalf("без сделок в средние не входит: %+v", s)
	}
}

func TestОкноЕщёНеЗакончилось(t *testing.T) {
	obs, _ := Evaluate(Bond{SecID: "B", Issue: day(8)}, fake{}, lqdt(10), []int{5})
	if obs != nil {
		t.Fatalf("%+v", obs)
	}
}
