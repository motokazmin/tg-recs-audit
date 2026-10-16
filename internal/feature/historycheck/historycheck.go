// Package historycheck — хватает ли ценовой истории, чтобы проверять рекомендации.
//
// На каждый тикер: есть ли бумага, покрыт ли период упоминаний, торгуется ли она
// сейчас, где дыры, где подозрение на сплит (цены ISS не скорректированы).
package historycheck

import (
	"time"

	"tg-recs-audit/internal/contract"
)

var (
	// Биржа не торговала акциями с 2022-02-28; 33 бумаги вернулись 03-24, остальные к 03-30.
	// Дыра, целиком лежащая в этом окне, — не проблема данных.
	marketHalts = [][2]time.Time{{day(2022, 2, 25), day(2022, 3, 31)}}
)

const (
	gapDays       = 14  // календарных дней без торгов — уже дыра (новогодние дают до ~11)
	splitRatio    = 3.0 // close за день изменился больше чем втрое — сплит или консолидация
	staleAfterDay = 14  // нет торгов дольше — бумага не торгуется (делистинг или смена тикера)
)

type Status string

const (
	OK         Status = "ok"
	Attention  Status = "ВНИМАНИЕ"
	NotCovered Status = "НЕ ПОКРЫТ"
	Missing    Status = "НЕТ"
)

type Split struct {
	Day   time.Time
	Ratio float64
}

type Report struct {
	SecID       string
	Need        [2]time.Time // первое и последнее упоминание
	Found       bool
	First, Last time.Time
	Days        int
	Stale       bool // давно нет торгов: делистинг или смена тикера
	Covers      bool // история начинается не позже первого упоминания и не кончается раньше него
	Gaps        [][2]time.Time
	Splits      []Split
}

func (r Report) Status() Status {
	switch {
	case !r.Found:
		return Missing
	case !r.Covers:
		return NotCovered
	case r.Stale || len(r.Gaps) > 0 || len(r.Splits) > 0:
		return Attention
	}
	return OK
}

// Check проверяет каждый тикер на отрезке need (первое…последнее упоминание).
func Check(need map[string][2]time.Time, h contract.PriceHistory, today time.Time) ([]Report, error) {
	out := make([]Report, 0, len(need))
	for secid, period := range need {
		bars, err := h.DailyBars(secid)
		if err != nil {
			return nil, err
		}
		out = append(out, checkOne(secid, period, bars, today))
	}
	return out, nil
}

func checkOne(secid string, need [2]time.Time, bars []contract.Bar, today time.Time) Report {
	r := Report{SecID: secid, Need: need}
	if len(bars) == 0 {
		return r
	}
	lo, hi := need[0], need[1]
	r.Found = true
	r.First, r.Last, r.Days = bars[0].Day, bars[len(bars)-1].Day, len(bars)
	r.Stale = today.Sub(r.Last) > staleAfterDay*24*time.Hour
	// Торгуется сейчас — покрыт до конца. Перестала торговаться — покрыт, если
	// торговалась на момент первого упоминания: дальше это вопрос политики выхода.
	r.Covers = !r.First.After(lo) && !r.Last.Before(lo)

	for i := 1; i < len(bars); i++ {
		a, b := bars[i-1], bars[i]
		if b.Day.Sub(a.Day) > gapDays*24*time.Hour && !insideHalt(a.Day, b.Day) &&
			!b.Day.Before(lo) && !a.Day.After(hi) {
			r.Gaps = append(r.Gaps, [2]time.Time{a.Day, b.Day})
		}
		// Сплит после первого упоминания мешает всегда: позицию держим до продажи.
		if k := b.Close / a.Close; (k > splitRatio || k < 1/splitRatio) && !b.Day.Before(lo) {
			r.Splits = append(r.Splits, Split{b.Day, k})
		}
	}
	return r
}

func insideHalt(a, b time.Time) bool {
	for _, h := range marketHalts {
		if !a.AddDate(0, 0, 1).Before(h[0]) && !b.AddDate(0, 0, -1).After(h[1]) {
			return true
		}
	}
	return false
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
