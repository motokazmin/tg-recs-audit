package portfolio

import (
	"strings"
	"testing"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/feature/ideas"
)

type fakeDivs map[string][]contract.DividendRow

func (f fakeDivs) Dividends(s string) ([]contract.DividendRow, error) { return f[s], nil }

func runDivs(t *testing.T, ii []ideas.Idea, p fakePrices, dv fakeDivs, ca contract.CorpActions, asOf string) Result {
	t.Helper()
	r, err := Run(ii, p, dv, nil, ca, params, d(asOf))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

var sberBars = fakePrices{"SBER": {
	bar("2024-07-09", 100, 101, 99, 100),
	bar("2024-07-10", 100, 101, 99, 100), // T-1
	bar("2024-07-11", 90, 91, 89, 90),    // отсечка: цена падает на дивиденд
	bar("2024-07-12", 90, 91, 89, 90),
}}

func TestДивидендНачисляетсяЕслиДержалиНаЗакрытииTminus1(t *testing.T) {
	dv := fakeDivs{"SBER": {{SecID: "SBER", LastBuyDay: "2024-07-10", Amount: 10}}}
	pos := runDivs(t, []ideas.Idea{idea(1, "2024-07-08T12:00", "SBER", 200)}, sberBars, dv, contract.CorpActions{}, "2024-07-12").Positions[0]
	if len(pos.Dividends) != 1 || pos.DividendSum() != 10*pos.Qty {
		t.Fatalf("%+v", pos)
	}
}

func TestКупленныйВДеньTminus1ПолучаетАПослеНего_нет(t *testing.T) {
	dv := fakeDivs{"SBER": {{SecID: "SBER", LastBuyDay: "2024-07-10", Amount: 10}}}
	pos := runDivs(t, []ideas.Idea{idea(1, "2024-07-09T12:00", "SBER", 200)}, sberBars, dv, contract.CorpActions{}, "2024-07-12").Positions[0]
	if len(pos.Dividends) != 1 {
		t.Fatalf("вход по open 07-10 = T-1, на закрытии держим: %+v", pos)
	}
	pos = runDivs(t, []ideas.Idea{idea(1, "2024-07-10T12:00", "SBER", 200)}, sberBars, dv, contract.CorpActions{}, "2024-07-12").Positions[0]
	if len(pos.Dividends) != 0 {
		t.Fatalf("вход 07-11 — после T-1: %+v", pos)
	}
}

func TestПродажаВДеньTminus1БезДивиденда(t *testing.T) {
	p := fakePrices{"SBER": {
		bar("2024-12-27", 100, 101, 99, 100),
		bar("2024-12-30", 100, 101, 99, 100), // T-1 и последний день года: продаём на close
		bar("2025-01-03", 90, 91, 89, 90),
	}}
	dv := fakeDivs{"SBER": {{SecID: "SBER", LastBuyDay: "2024-12-30", Amount: 10}}}
	pos := runDivs(t, []ideas.Idea{idea(1, "2024-12-26T12:00", "SBER", 200)}, p, dv, contract.CorpActions{}, "2025-01-03").Positions[0]
	if pos.ExitReason != "горизонт" || len(pos.Dividends) != 0 {
		t.Fatalf("продали на закрытии T-1 — дивиденд покупателю: %+v", pos)
	}
}

func TestМасштабИсточникаПриводитсяСплитамиПослеВыплаты(t *testing.T) {
	p := fakePrices{"GMKN": {
		bar("2023-12-22", 17000, 17100, 16900, 17000),
		bar("2023-12-25", 17000, 17100, 16900, 17000), // T-1
		bar("2023-12-26", 16100, 16200, 16000, 16100),
	}}
	// источник пересчитал под сплит 2024-04-08 ×100: 9.153 вместо 915.33
	dv := fakeDivs{"GMKN": {{SecID: "GMKN", LastBuyDay: "2023-12-25", Amount: 9.1533}}}
	ca := contract.CorpActions{Splits: []contract.Split{{SecID: "GMKN", Day: "2024-04-08", Factor: 100}}}
	pos := runDivs(t, []ideas.Idea{idea(1, "2023-12-21T12:00", "GMKN", 30000)}, p, dv, ca, "2023-12-26").Positions[0]
	if len(pos.Dividends) != 1 || pos.Dividends[0].PerShare < 915 || pos.Dividends[0].PerShare > 916 {
		t.Fatalf("на акцию 2023 года — 915.33: %+v", pos.Dividends)
	}

	r := runDivs(t, []ideas.Idea{idea(1, "2023-12-21T12:00", "GMKN", 30000)}, p, dv, contract.CorpActions{}, "2023-12-26")
	if len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "масштаб не сходится") {
		t.Fatalf("без сплита в таблице доходность 0.054%% — проблема: %+v", r)
	}
}

func TestПодтверждениеОднойИзДвухВыплатОдногоДня(t *testing.T) {
	// POSI 2024-05-24: 47.33 и 4.56; 4.56 (0.15%) не проходит сверку — подтверждена вручную
	p := fakePrices{"POSI": {bar("2024-05-22", 3135, 3140, 3130, 3135), bar("2024-05-23", 3135, 3140, 3130, 3135), bar("2024-05-24", 3080, 3090, 3070, 3080)}}
	dv := fakeDivs{"POSI": {{SecID: "POSI", LastBuyDay: "2024-05-23", Amount: 47.33}, {SecID: "POSI", LastBuyDay: "2024-05-23", Amount: 4.56}}}
	ii := []ideas.Idea{idea(1, "2024-05-21T12:00", "POSI", 5000)}
	if r := runDivs(t, ii, p, dv, contract.CorpActions{}, "2024-05-24"); len(r.Problems) != 1 {
		t.Fatalf("0.15%% без подтверждения — проблема: %+v", r)
	}
	ca := contract.CorpActions{Dividends: []contract.DividendFix{{SecID: "POSI", LastBuyDay: "2024-05-23", Source: 4.56, Amount: 4.56}}}
	pos := runDivs(t, ii, p, dv, ca, "2024-05-24").Positions[0]
	if len(pos.Dividends) != 2 || pos.Dividends[0].PerShare != 47.33 || pos.Dividends[1].PerShare != 4.56 {
		t.Fatalf("правка одной строки не трогает соседнюю: %+v", pos.Dividends)
	}
}

func TestРучнаяПоправкаДивиденда(t *testing.T) {
	dv := fakeDivs{"SBER": {{SecID: "SBER", LastBuyDay: "2024-07-10", Amount: 10}}}
	ca := contract.CorpActions{Dividends: []contract.DividendFix{{SecID: "SBER", LastBuyDay: "2024-07-10", Amount: 7}}}
	pos := runDivs(t, []ideas.Idea{idea(1, "2024-07-08T12:00", "SBER", 200)}, sberBars, dv, ca, "2024-07-12").Positions[0]
	if len(pos.Dividends) != 1 || pos.Dividends[0].PerShare != 7 {
		t.Fatalf("%+v", pos.Dividends)
	}
}

func TestСтарыйКодБерётВыплатыПреемника(t *testing.T) {
	p := fakePrices{
		"TCSG": {bar("2024-11-20", 2500, 2510, 2490, 2500), bar("2024-11-21", 2500, 2510, 2490, 2500), bar("2024-11-22", 2400, 2410, 2390, 2400)},
		"T":    {bar("2024-11-28", 2520, 2600, 2500, 2550)},
	}
	// страницы TCSG у источника нет, история — у T, в масштабе после сплита T ×10 (2026)
	dv := fakeDivs{"T": {{SecID: "T", LastBuyDay: "2024-11-21", Amount: 9.25}}}
	ca := contract.CorpActions{
		Renames: []contract.Rename{{From: "TCSG", To: "T", Ratio: 1}},
		Splits:  []contract.Split{{SecID: "T", Day: "2026-04-17", Factor: 10}},
	}
	pos := runDivs(t, []ideas.Idea{idea(1, "2024-11-19T12:00", "TCSG", 9000)}, p, dv, ca, "2024-12-01").Positions[0]
	if len(pos.Dividends) != 1 || pos.Dividends[0].PerShare != 92.5 || pos.Dividends[0].SecID != "TCSG" {
		t.Fatalf("%+v", pos.Dividends)
	}
}
