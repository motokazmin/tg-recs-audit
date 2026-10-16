package paperstrat

import (
	"testing"
	"time"

	"tg-recs-audit/internal/contract"
)

type chan1 struct{ msgs []contract.Message }

func (c chan1) Messages() (contract.ChannelInfo, []contract.Message, error) {
	return contract.ChannelInfo{}, c.msgs, nil
}

type prices map[string][]contract.Bar

func (p prices) DailyBars(s string) ([]contract.Bar, error) { return p[s], nil }

type divs map[string][]contract.DividendRow

func (d divs) Dividends(s string) ([]contract.DividendRow, error) { return d[s], nil }

type mem map[string]string

func (m mem) Get(k string) (string, bool) { v, ok := m[k]; return v, ok }
func (m mem) Set(k, v string)             { m[k] = v }
func (m mem) Keys(p string) []string {
	var out []string
	for k := range m {
		if len(k) >= len(p) && k[:len(p)] == p {
			out = append(out, k)
		}
	}
	return out
}

func dd(s string) time.Time { t, _ := time.Parse(time.DateOnly, s); return t }

func TestСтароеСобытиеПолучаетПодтверждённыйДивиденд(t *testing.T) {
	s := &Dividends{StartDay: dd("2026-06-01"), Cap: 1e6, PerTrade: 50000,
		Source: chan1{[]contract.Message{{ID: 1, PostedAt: dd("2026-06-02").Add(10 * time.Hour),
			Text: "Рекомендованы дивиденды X\nДивиденд: 10 руб.\nПоследний день покупки: 09.07.2026\n#STSB"}}},
		Known:  map[string]bool{"STSB": true},
		Prices: prices{"STSB": {{Day: dd("2026-06-03"), Open: 100, Close: 100}, {Day: dd("2026-07-09"), Close: 100}, {Day: dd("2026-07-10"), Close: 90}}},
		Divs:   divs{"STSB": {{SecID: "STSB", LastBuyDay: "2026-07-09", Amount: 10}}},
	}
	// Первый запуск через 98 дней после отсечки — выплата подтверждена, обязана зачислиться.
	ops, err := s.Ops(dd("2026-10-16"), mem{})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, o := range ops {
		kinds[o.Kind]++
	}
	if kinds["buy"] != 1 || kinds["sell"] != 1 || kinds["dividend"] != 1 {
		t.Fatalf("%+v", ops)
	}
}
