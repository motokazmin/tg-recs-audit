package divevents

import (
	"testing"
	"time"

	"tg-recs-audit/internal/contract"
)

func TestРазборПостаРекомендации(t *testing.T) {
	known := map[string]bool{"PIKK": true, "SNGS": true, "SNGSP": true, "NLMK": true}
	msgs := []contract.Message{
		{ID: 1, PostedAt: time.Date(2021, 4, 5, 7, 0, 0, 0, time.UTC), Text: "Рекомендованы дивиденды ПИК за 2020 г. и I кв. 2021 г.\n\nСуммарный дивиденд: 22,51+22,92= 45,43 руб.\nДивидендная доходность: 5,04%\nПоследний день покупки: 13 мая 2021\n\n#PIKK"},
		{ID: 2, PostedAt: time.Date(2024, 4, 10, 14, 0, 0, 0, time.UTC), Text: "Рекомендованы дивиденды НЛМК за 2023 г.\n\nЦена на акцию: 220 руб.\nДивидендная доходность: 11,56%\nДивиденд на акцию: 25,43 руб.\nПоследний день покупки: 24.05.2024\n#NLMK"},
		{ID: 3, PostedAt: time.Date(2024, 4, 11, 14, 0, 0, 0, time.UTC), Text: "Рекомендованы дивиденды НЛМК за 2023 г.\nДивиденд на акцию: 25,43 руб.\nПоследний день покупки: 24.05.2024\n#NLMK"}, // повтор
		{ID: 4, PostedAt: time.Date(2024, 6, 1, 14, 0, 0, 0, time.UTC), Text: "Рекомендованы дивиденды Сургутнефтегаз\nДивиденд: 0,85 руб.\nПоследний день покупки: 16.07.2024\n#SNGSP #SNGS"},
	}
	ev, un := Parse(msgs, known)
	if len(un) != 0 || len(ev) != 3 {
		t.Fatalf("события %+v, отказы %+v", ev, un)
	}
	if ev[0].Div != 45.43 || !ev[0].Interim || ev[0].LastBuy != time.Date(2021, 5, 13, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("сумма после «=», дата словами: %+v", ev[0])
	}
	if ev[1].Div != 25.43 || ev[1].Yield != 11.56 {
		t.Fatalf("строка доходности раньше суммы — не сумма: %+v", ev[1])
	}
	if ev[2].SecID != "SNGS" {
		t.Fatalf("есть и обыкновенные, и префы — берём обыкновенные: %+v", ev[2])
	}
}

func TestНевыплаченныйДивидендНеНачисляется(t *testing.T) {
	d := func(s string) time.Time { x, _ := time.Parse(time.DateOnly, s); return x }
	bars := []contract.Bar{{Day: d("2024-05-02"), Open: 100, Close: 100}, {Day: d("2024-05-24"), Close: 110}, {Day: d("2024-05-27"), Close: 99}}
	idx := []contract.Bar{{Day: d("2024-05-02"), Open: 1000, Close: 1000}, {Day: d("2024-05-24"), Close: 1000}, {Day: d("2024-05-27"), Close: 1000}}
	e := Event{PostedAt: d("2024-05-01").Add(15 * time.Hour), SecID: "X", Div: 10, LastBuy: d("2024-05-24")}
	if r := Evaluate(e, bars, idx, true); r.RetB < 0.08 {
		t.Fatalf("выплачен: %+v", r)
	}
	if r := Evaluate(e, bars, idx, false); r.RetB > -0.005 {
		t.Fatalf("отменён собранием — дивиденда нет: %+v", r)
	}
}
