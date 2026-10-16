package paper

import (
	"math"
	"sort"
	"time"

	"tg-recs-audit/internal/contract"
)

// Point — капитал счёта на конец дня и тот же стартовый капитал в бенчмарке.
type Point struct {
	Day       time.Time
	Cash      float64
	Positions float64
	Bench     float64
}

func (p Point) Equity() float64 { return p.Cash + p.Positions }

// Trade — сделка из журнала: покупка, доходы по бумаге, продажа (одна идея — одна бумага).
type Trade struct {
	SecID    string
	PostID   int64
	Open     time.Time
	Close    time.Time // нулевое — позиция открыта
	Cost     float64   // руб., с издержками
	Proceeds float64   // руб.: продажа + дивиденды/купоны/амортизация; у открытой — по оценке
	Return   float64
	BenchRet float64 // бенчмарк за то же окно
	Qty      float64
	Reason   string
}

// Excess — доходность сделки минус бенчмарк за то же окно.
func (t Trade) Excess() float64 { return t.Return - t.BenchRet }

func day(s string) time.Time { t, _ := time.Parse(time.DateOnly, s); return t }

// Curve — капитал по дням бенчмарка со старта по asOf. Операции копятся до дня включительно:
// зачисление бывает и в неторговый день (подтверждение выплаты увидено в выходной).
func Curve(s Strategy, ops []contract.Op, bench []contract.Bar, asOf time.Time) []Point {
	sorted := append([]contract.Op(nil), ops...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Day < sorted[j].Day })
	start := s.Start()
	var b0 float64
	var out []Point
	cash := s.Capital()
	qty := map[string]float64{}
	k := 0
	for _, b := range bench {
		if b.Day.Before(start) || !b.Day.Before(asOf) {
			continue
		}
		if b0 == 0 {
			b0 = b.Close
		}
		d := b.Day.Format(time.DateOnly)
		for ; k < len(sorted) && sorted[k].Day <= d; k++ {
			o := sorted[k]
			cash += o.Amount
			switch o.Kind {
			case "buy":
				qty[o.SecID] += o.Qty
			case "sell":
				qty[o.SecID] -= o.Qty
			}
		}
		p := Point{Day: b.Day, Cash: cash, Bench: s.Capital() * b.Close / b0}
		for secid, q := range qty {
			if q > 1e-9 {
				if px, ok := s.Mark(secid, b.Day); ok {
					p.Positions += q * px
				}
			}
		}
		out = append(out, p)
	}
	// Операции дней, за которые бара бенчмарка ещё нет (история ISS — после вечерней сессии),
	// всё равно в счёте: последняя точка — по последнему известному бенчмарку.
	if k < len(sorted) && len(out) > 0 {
		last := out[len(out)-1]
		for ; k < len(sorted) && sorted[k].Day < asOf.Format(time.DateOnly); k++ {
			o := sorted[k]
			cash += o.Amount
			switch o.Kind {
			case "buy":
				qty[o.SecID] += o.Qty
			case "sell":
				qty[o.SecID] -= o.Qty
			}
		}
		p := Point{Day: day(sorted[k-1].Day), Cash: cash, Bench: last.Bench}
		for secid, q := range qty {
			if q > 1e-9 {
				if px, ok := s.Mark(secid, p.Day); ok {
					p.Positions += q * px
				}
			}
		}
		out = append(out, p)
	}
	return out
}

// Trades собирает сделки из журнала: по (пост, бумага).
func Trades(s Strategy, ops []contract.Op, bench []contract.Bar, asOf time.Time) []Trade {
	type k struct {
		post  int64
		secid string
	}
	m := map[k]*Trade{}
	var order []k
	for _, o := range ops {
		if o.Kind == "skip" || o.SecID == "" {
			continue
		}
		key := k{o.PostID, o.SecID}
		t := m[key]
		if t == nil {
			t = &Trade{SecID: o.SecID, PostID: o.PostID}
			m[key] = t
			order = append(order, key)
		}
		switch o.Kind {
		case "buy":
			t.Open, t.Cost, t.Qty = day(o.Day), t.Cost-o.Amount, t.Qty+o.Qty
		case "sell":
			t.Close, t.Reason = day(o.Day), o.Reason
			t.Proceeds += o.Amount
		default: // dividend, coupon, amortization
			t.Proceeds += o.Amount
		}
	}
	var out []Trade
	for _, key := range order {
		t := *m[key]
		if t.Cost <= 0 {
			continue
		}
		end := t.Close
		if end.IsZero() {
			end = lastBefore(bench, asOf)
			if px, ok := s.Mark(t.SecID, end); ok {
				t.Proceeds += t.Qty * px
			}
		}
		t.Return = t.Proceeds/t.Cost - 1
		t.BenchRet = closeAt(bench, end)/openAt(bench, t.Open) - 1
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Open.Before(out[j].Open) })
	return out
}

// Summary — превышение над бенчмарком по закрытым сделкам.
type Summary struct {
	Closed, Open int
	Mean, Median float64
	Positive     float64
	MeanRet      float64
}

func Summarize(ts []Trade) Summary {
	var s Summary
	var ex []float64
	for _, t := range ts {
		if t.Close.IsZero() {
			s.Open++
			continue
		}
		e := t.Excess()
		if math.IsNaN(e) {
			continue
		}
		ex = append(ex, e)
		s.MeanRet += t.Return
	}
	s.Closed = len(ex)
	if s.Closed == 0 {
		return s
	}
	var pos int
	for _, e := range ex {
		s.Mean += e
		if e > 0 {
			pos++
		}
	}
	s.Mean /= float64(s.Closed)
	s.MeanRet /= float64(s.Closed)
	s.Positive = float64(pos) / float64(s.Closed)
	sort.Float64s(ex)
	s.Median = ex[len(ex)/2]
	return s
}

func lastBefore(bars []contract.Bar, d time.Time) time.Time {
	i := sort.Search(len(bars), func(i int) bool { return !bars[i].Day.Before(d) }) - 1
	if i < 0 {
		return d
	}
	return bars[i].Day
}

func closeAt(bars []contract.Bar, d time.Time) float64 {
	i := sort.Search(len(bars), func(i int) bool { return bars[i].Day.After(d) }) - 1
	if i < 0 {
		return math.NaN()
	}
	return bars[i].Close
}

func openAt(bars []contract.Bar, d time.Time) float64 {
	i := sort.Search(len(bars), func(i int) bool { return !bars[i].Day.Before(d) })
	if i == len(bars) {
		return math.NaN()
	}
	if bars[i].Day.Equal(d) && bars[i].Open > 0 {
		return bars[i].Open
	}
	return bars[i].Close
}
