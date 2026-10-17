package placements

import (
	"math"
	"sort"
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

// Флоатер и погашенный выпуск — без купона в карточке, но с COUPONFREQUENCY: обычные выпуски.
// Ноты без купона — нет, даже с COUPONFREQUENCY (0035).
func TestОтборБезКупонаВКарточке(t *testing.T) {
	base := map[string]string{"ISSUEDATE": "2025-01-10", "FACEUNIT": "SUR", "INITIALFACEVALUE": "1000", "ISSUESIZE": "1000"}
	cards := map[string]map[string]string{
		"RU000A10A001": {"NAME": "АЛРОСА 002Р-03", "COUPONFREQUENCY": "12"},
		"RU000A10A002": {"NAME": "ВЭБ.РФ ПБО-002Р-К470", "COUPONFREQUENCY": "4"},
		"RU000A10A003": {"NAME": "Сбер CIB-SN-1", "COUPONFREQUENCY": "1"},
		"RU000A10A004": {"NAME": "Какая-то нота"},
		"RU000A10A005": {"NAME": "Магнит БО-005Р-05", "COUPONPERCENT": "18.5", "COUPONFREQUENCY": "12"},
	}
	var all []contract.Security
	for id, c := range cards {
		for k, v := range base {
			c[k] = v
		}
		all = append(all, contract.Security{SecID: id, Type: "corporate_bond"})
	}
	got, _, err := Universe(all, func(id string) (map[string]string, error) { return cards[id], nil }, day(0), day(30))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, b := range got {
		ids = append(ids, b.SecID)
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "RU000A10A001" || ids[1] != "RU000A10A005" {
		t.Fatalf("отобраны %v, ждали флоатер АЛРОСА и Магнит", ids)
	}
}
