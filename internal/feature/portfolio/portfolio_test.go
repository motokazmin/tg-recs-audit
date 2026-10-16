package portfolio

import (
	"strings"
	"testing"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/feature/ideas"
)

type fakePrices map[string][]contract.Bar

func (f fakePrices) DailyBars(s string) ([]contract.Bar, error) { return f[s], nil }

func d(s string) time.Time { t, _ := time.Parse(time.DateOnly, s); return t }

// bar: open, high, low, close
func bar(day string, o, h, l, c float64) contract.Bar {
	return contract.Bar{Day: d(day), Open: o, High: h, Low: l, Close: c}
}

func idea(id int64, posted string, secid string, target float64) ideas.Idea {
	t, _ := time.Parse("2006-01-02T15:04", posted)
	return ideas.Idea{PostID: id, PostedAt: t.Add(-3 * time.Hour), SecID: secid, Target: target} // время поста — московское
}

var params = Params{PerIdea: 1000, Horizon: YearEnd}

func run(t *testing.T, ii []ideas.Idea, p fakePrices, ca contract.CorpActions, asOf string) Result {
	t.Helper()
	r, err := Run(ii, p, nil, nil, ca, params, d(asOf))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestВходПоOpenСледующегоДняИВыходПоЦели(t *testing.T) {
	p := fakePrices{"SBER": {
		bar("2024-01-10", 100, 130, 99, 100), // день поста: high выше цели, но пост вышел в 09:03 — не наш
		bar("2024-01-11", 101, 105, 100, 104),
		bar("2024-01-12", 104, 111, 103, 108),
	}}
	r := run(t, []ideas.Idea{idea(1, "2024-01-10T09:03", "SBER", 110)}, p, contract.CorpActions{}, "2024-01-12")
	if len(r.Positions) != 1 {
		t.Fatalf("%+v", r)
	}
	pos := r.Positions[0]
	if !pos.EntryDay.Equal(d("2024-01-11")) || pos.EntryPrice != 101 || pos.Qty != 9 {
		t.Fatalf("вход: %+v", pos)
	}
	if pos.ExitReason != "цель" || pos.ExitPrice != 110 || !pos.ExitDay.Equal(d("2024-01-12")) {
		t.Fatalf("выход: %+v", pos)
	}
}

func TestЦельНаГэпеИсполняетсяПоOpen(t *testing.T) {
	p := fakePrices{"SBER": {bar("2024-01-11", 100, 101, 99, 100), bar("2024-01-12", 115, 120, 114, 118)}}
	pos := run(t, []ideas.Idea{idea(1, "2024-01-10T12:00", "SBER", 110)}, p, contract.CorpActions{}, "2024-01-12").Positions[0]
	if pos.ExitReason != "цель (гэп)" || pos.ExitPrice != 115 {
		t.Fatalf("%+v", pos)
	}
}

func TestГоризонтКонецГода(t *testing.T) {
	p := fakePrices{"SBER": {
		bar("2024-12-27", 100, 101, 99, 100),
		bar("2024-12-30", 100, 101, 99, 97),
		bar("2025-01-03", 98, 99, 97, 98),
	}}
	pos := run(t, []ideas.Idea{idea(1, "2024-12-26T12:00", "SBER", 200)}, p, contract.CorpActions{}, "2025-01-03").Positions[0]
	if pos.ExitReason != "горизонт" || !pos.ExitDay.Equal(d("2024-12-30")) || pos.ExitPrice != 97 {
		t.Fatalf("%+v", pos)
	}
}

func TestГоризонтМесяц(t *testing.T) {
	p := fakePrices{"SBER": {
		bar("2024-01-11", 100, 101, 99, 100), // вход
		bar("2024-02-09", 100, 101, 99, 103),
		bar("2024-02-12", 100, 101, 99, 105), // уже за горизонтом 2024-02-11
	}}
	r, err := Run([]ideas.Idea{idea(1, "2024-01-10T12:00", "SBER", 200)}, p, nil, nil, contract.CorpActions{}, Params{PerIdea: 1000, Horizon: "1m"}, d("2024-02-12"))
	if err != nil {
		t.Fatal(err)
	}
	pos := r.Positions[0]
	if pos.ExitReason != "горизонт" || !pos.ExitDay.Equal(d("2024-02-09")) || pos.ExitPrice != 103 {
		t.Fatalf("месяц от 2024-01-11 — по 2024-02-11, продажа по close 02-09: %+v", pos)
	}
	if _, err := ParseHorizon("10"); err == nil {
		t.Fatal("горизонт без единицы — ошибка")
	}
}

func TestПовторнаяИдеяПокаВПортфелеПропускается(t *testing.T) {
	p := fakePrices{"SBER": {bar("2024-01-11", 100, 101, 99, 100), bar("2024-01-12", 100, 101, 99, 100), bar("2024-01-15", 100, 120, 99, 100), bar("2024-01-16", 100, 101, 99, 100)}}
	r := run(t, []ideas.Idea{
		idea(1, "2024-01-10T12:00", "SBER", 110),
		idea(2, "2024-01-11T12:00", "SBER", 120), // первая ещё держится
		idea(3, "2024-01-15T12:00", "SBER", 130), // первая закрылась 15-го по цели, вход 16-го
	}, p, contract.CorpActions{}, "2024-01-16")
	if len(r.Positions) != 2 || len(r.Skips) != 1 || !strings.HasPrefix(r.Skips[0].Reason, "уже в портфеле") {
		t.Fatalf("%+v", r)
	}
}

func TestЦельДостигнутаДоВхода(t *testing.T) {
	p := fakePrices{"SBER": {bar("2024-01-11", 112, 113, 111, 112)}}
	r := run(t, []ideas.Idea{idea(1, "2024-01-10T12:00", "SBER", 110)}, p, contract.CorpActions{}, "2024-01-11")
	if len(r.Positions) != 0 || len(r.Skips) != 1 || r.Skips[0].Reason != "цель достигнута до входа" {
		t.Fatalf("%+v", r)
	}
}

func TestСплитПоТаблицеИНеобъяснённыйСкачок(t *testing.T) {
	p := fakePrices{"GMKN": {bar("2024-04-01", 15000, 15100, 14900, 15000), bar("2024-04-08", 152, 153, 150, 151)}}
	ii := []ideas.Idea{idea(1, "2024-03-29T12:00", "GMKN", 20000)}

	r := run(t, ii, p, contract.CorpActions{}, "2024-04-08")
	if len(r.Problems) != 1 || len(r.Positions) != 0 {
		t.Fatalf("скачок без записи в таблице обязан быть проблемой: %+v", r)
	}

	ca := contract.CorpActions{Splits: []contract.Split{{SecID: "GMKN", Day: "2024-04-08", Factor: 100}}}
	pos := run(t, ii, p, ca, "2024-04-08").Positions[0]
	if !pos.Open() || pos.Proceeds != 100*151 || pos.Qty != 1 {
		t.Fatalf("1 акция до сплита = 100 после: %+v", pos)
	}
}

func TestПереименованиеПродолжаетПозицию(t *testing.T) {
	p := fakePrices{
		"TCSG": {bar("2024-11-26", 2500, 2510, 2490, 2500), bar("2024-11-27", 2500, 2510, 2490, 2500)},
		"T":    {bar("2024-11-28", 2520, 2600, 2500, 2550), bar("2024-12-02", 2600, 2700, 2590, 2650)},
	}
	ca := contract.CorpActions{Renames: []contract.Rename{{From: "TCSG", To: "T", Ratio: 1}}}
	pos := run(t, []ideas.Idea{idea(1, "2024-11-25T12:00", "TCSG", 2650)}, p, ca, "2024-12-20").Positions[0]
	if pos.SecID != "T" || pos.ExitReason != "цель" || !pos.ExitDay.Equal(d("2024-12-02")) {
		t.Fatalf("%+v", pos)
	}
}

func TestПрекращениеТоргов(t *testing.T) {
	p := fakePrices{"POLY": {bar("2024-09-20", 300, 301, 299, 300), bar("2024-09-23", 290, 291, 280, 285)}}
	ii := []ideas.Idea{idea(1, "2024-09-19T12:00", "POLY", 500)}
	if r := run(t, ii, p, contract.CorpActions{}, "2024-12-01"); len(r.Problems) != 1 {
		t.Fatalf("бумага пропала без записи в таблице — проблема, а не тихая оценка: %+v", r)
	}
	ca := contract.CorpActions{Frozen: []contract.Frozen{{SecID: "POLY"}}}
	pos := run(t, ii, p, ca, "2024-12-01").Positions[0]
	if pos.ExitReason != "торги прекращены" || pos.ExitPrice != 285 {
		t.Fatalf("%+v", pos)
	}
}

func TestПостБезСледующегоТорговогоДняЖдёт(t *testing.T) {
	p := fakePrices{"SBER": {bar("2024-01-10", 100, 101, 99, 100)}}
	r := run(t, []ideas.Idea{idea(1, "2024-01-10T12:00", "SBER", 110)}, p, contract.CorpActions{}, "2024-01-10")
	if len(r.Pending) != 1 || len(r.Positions) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestAsOfНеВидитБудущего(t *testing.T) {
	p := fakePrices{"SBER": {bar("2024-01-11", 100, 101, 99, 100), bar("2024-01-12", 100, 120, 99, 100)}}
	pos := run(t, []ideas.Idea{idea(1, "2024-01-10T12:00", "SBER", 110)}, p, contract.CorpActions{}, "2024-01-11").Positions[0]
	if !pos.Open() {
		t.Fatalf("на 2024-01-11 цель ещё не достигнута: %+v", pos)
	}
}

func TestНетИсторииЭтоПропускАНеОжидание(t *testing.T) {
	r := run(t, []ideas.Idea{idea(1, "2023-01-10T12:00", "POLY", 800)}, fakePrices{}, contract.CorpActions{}, "2026-10-16")
	if len(r.Pending) != 0 || len(r.Skips) != 1 || r.Skips[0].Reason != "нет истории на MOEX" {
		t.Fatalf("%+v", r)
	}
}

func TestДеньСплитаСудитсяТолькоПоClose(t *testing.T) {
	// BELU 2024-08-22: open/high в старом масштабе, close в новом
	p := fakePrices{"BELU": {
		bar("2024-04-24", 5982, 6000, 5900, 5990),
		bar("2024-08-15", 4700, 4767, 4400, 4680),
		bar("2024-08-22", 4223, 4223, 651, 714),
	}}
	ca := contract.CorpActions{Splits: []contract.Split{{SecID: "BELU", Day: "2024-08-22", Factor: 8}}}
	pos := run(t, []ideas.Idea{idea(1, "2024-04-23T12:00", "BELU", 7000)}, p, ca, "2024-08-22").Positions[0]
	if !pos.Open() || pos.Proceeds != 8*714 {
		t.Fatalf("open старого масштаба в день сплита не цель: %+v", pos)
	}
}
