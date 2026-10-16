package diary

import (
	"fmt"
	"math"
	"testing"
	"time"

	"tg-recs-audit/internal/contract"
)

type fakeMarket struct {
	inst   map[string]contract.Instrument
	shares map[string][]contract.Bar
	bonds  map[string][]contract.BondBar
	flows  map[string][]contract.BondFlow
}

func (f fakeMarket) Resolve(ref string) (contract.Instrument, error) {
	if in, ok := f.inst[ref]; ok {
		return in, nil
	}
	return contract.Instrument{}, fmt.Errorf("нет %s", ref)
}
func (f fakeMarket) DailyBars(s string) ([]contract.Bar, error)      { return f.shares[s], nil }
func (f fakeMarket) BondBars(s string) ([]contract.BondBar, error)   { return f.bonds[s], nil }
func (f fakeMarket) BondFlows(s string) ([]contract.BondFlow, error) { return f.flows[s], nil }

func d(s string) time.Time { t, _ := time.Parse(time.DateOnly, s); return t }

// Календарь: рабочие дни, бенчмарк и деньги — константы.
func calendar(from, to string) []contract.Bar {
	var out []contract.Bar
	for t := d(from); !t.After(d(to)); t = t.AddDate(0, 0, 1) {
		if t.Weekday() != time.Saturday && t.Weekday() != time.Sunday {
			out = append(out, contract.Bar{Day: t, Open: 100, High: 100, Low: 100, Close: 100})
		}
	}
	return out
}

func flat(from, to string, px float64) []contract.Bar {
	bars := calendar(from, to)
	for i := range bars {
		bars[i].Open, bars[i].High, bars[i].Low, bars[i].Close = px, px*1.01, px*0.99, px
	}
	return bars
}

func bondFlat(from, to string, pct float64) []contract.BondBar {
	var out []contract.BondBar
	for _, b := range calendar(from, to) {
		out = append(out, contract.BondBar{Day: b.Day, Open: pct, High: pct + 0.5, Low: pct - 0.5, Close: pct, Face: 1000, FaceUnit: "RUB"})
	}
	return out
}

func trade(id int64, day string, side Side, ref string, qty, amount float64) Trade {
	return Trade{PostID: id, PostedAt: d(day).Add(9 * time.Hour), Side: side, Ref: ref, Qty: qty, Amount: amount}
}

func runBoth(t *testing.T, m fakeMarket, ts []Trade, to string) (Result, Result) {
	t.Helper()
	deps := Deps{Resolver: m, Shares: m, Bonds: m, Bench: calendar("2024-01-01", to), MoneyMarket: calendar("2024-01-01", to)}
	cs := []Contribution{{PostID: 1, PostedAt: d("2024-01-01"), Amount: 1e6}}
	var out [2]Result
	for i, mode := range []Mode{Author, Follower} {
		r, err := Run(cs, ts, nil, nil, Params{Mode: mode}, deps)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Problems) > 0 {
			t.Fatalf("%s: %v", mode, r.Problems)
		}
		out[i] = r
	}
	return out[0], out[1]
}

func TestЦенаЗаШтукуВместоСуммы(t *testing.T) {
	// «1 лот (100шт) 32.67р»: «1 лот» сошёлся бы как одна акция за 32.67
	m := fakeMarket{inst: map[string]contract.Instrument{"SNGS": {SecID: "SNGS", Kind: "share"}},
		shares: map[string][]contract.Bar{"SNGS": flat("2024-01-01", "2024-01-10", 32.5)}}
	tr := trade(10, "2024-01-03", Buy, "SNGS", 100, 32.67)
	tr.QtyAlt = 1
	a, f := runBoth(t, m, []Trade{tr}, "2024-01-10")
	if a.Holdings["SNGS"] != 100 || f.Holdings["SNGS"] != 100 {
		t.Fatalf("автор %v, последователь %v", a.Holdings, f.Holdings)
	}
}

func TestРазмещениеШтукиИзСуммыУОбеихМоделей(t *testing.T) {
	m := fakeMarket{inst: map[string]contract.Instrument{"B": {SecID: "B", Kind: "bond"}},
		bonds: map[string][]contract.BondBar{"B": bondFlat("2024-01-08", "2024-01-20", 101)}}
	// отчёт об исполнении вышел после размещения: «насыпал на 120000р»
	tr := trade(10, "2024-01-09", Buy, "B", 0, 120000)
	tr.Placement = true
	a, f := runBoth(t, m, []Trade{tr}, "2024-01-20")
	if a.Holdings["B"] != 120 || f.Holdings["B"] != 120 {
		t.Fatalf("автор %v, последователь %v", a.Holdings, f.Holdings)
	}
	for _, r := range []Result{a, f} {
		for _, o := range r.Ops {
			if o.Kind == "buy" && (o.Price != 1000 || !o.Day.Equal(d("2024-01-08"))) {
				t.Fatalf("размещение — по номиналу в первый день торгов: %+v", o)
			}
		}
	}
}

func TestКупонТемКтоДержалНаКонецДняПередРеестром(t *testing.T) {
	m := fakeMarket{inst: map[string]contract.Instrument{"B": {SecID: "B", Kind: "bond"}},
		bonds: map[string][]contract.BondBar{"B": bondFlat("2024-01-01", "2024-01-31", 100)},
		flows: map[string][]contract.BondFlow{"B": {{Day: d("2024-01-16"), RecordDay: d("2024-01-15"), ValueRub: 20}}}}
	a, _ := runBoth(t, m, []Trade{trade(10, "2024-01-03", Buy, "B", 10, 10000)}, "2024-01-31")
	var got float64
	for _, o := range a.Ops {
		if o.Kind == "coupon" {
			got += o.Amount
		}
	}
	if got != 200 {
		t.Fatalf("10 бумаг × 20 ₽, а начислено %v", got)
	}
}

func TestОфертаПоНоминалуНезависимоОтРынка(t *testing.T) {
	m := fakeMarket{inst: map[string]contract.Instrument{"B": {SecID: "B", Kind: "bond"}},
		bonds: map[string][]contract.BondBar{"B": bondFlat("2024-01-01", "2024-01-31", 65)}}
	sell := Trade{PostID: 11, PostedAt: d("2024-01-20").Add(9 * time.Hour), Side: Sell, Ref: "B", All: true, Price: 1000, Fixed: true}
	a, f := runBoth(t, m, []Trade{trade(10, "2024-01-03", Buy, "B", 10, 6500), sell}, "2024-01-31")
	for _, r := range []Result{a, f} {
		last := r.Ops[len(r.Ops)-1]
		if last.Kind != "sell" || last.Price != 1000 || last.Amount != 10000 {
			t.Fatalf("оферта: %+v", last)
		}
	}
}

func TestПоследовательТеЖеШтукиНоПоOpenСледующегоДня(t *testing.T) {
	bars := flat("2024-01-01", "2024-01-10", 100)
	bars[3].Open = 110 // 2024-01-04 — день после поста
	m := fakeMarket{inst: map[string]contract.Instrument{"X": {SecID: "X", Kind: "share"}}, shares: map[string][]contract.Bar{"X": bars}}
	// «86 лотов (125шт) 8600р»: верны лоты — сверка выбирает их у обеих моделей
	tr := trade(10, "2024-01-03", Buy, "X", 125, 8600)
	tr.QtyAlt = 86
	a, f := runBoth(t, m, []Trade{tr}, "2024-01-10")
	if a.Holdings["X"] != 86 || f.Holdings["X"] != 86 || len(a.Warnings) != 0 {
		t.Fatalf("лоты — объяснённый вариант, без допущения: автор %v %v, последователь %v", a.Holdings, a.Warnings, f.Holdings)
	}
	for _, o := range f.Ops {
		if o.Kind == "buy" && (math.Abs(o.Price-110) > 1e-9 || !o.Day.Equal(d("2024-01-04"))) {
			t.Fatalf("последователь: %+v", o)
		}
	}
}
