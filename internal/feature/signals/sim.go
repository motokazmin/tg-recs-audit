package signals

import (
	"math"
	"sort"
	"time"

	"tg-recs-audit/internal/contract"
)

// Bars — 10-минутные свечи контракта за день (порт на стороне потребителя).
type Bars func(secid string, day time.Time) ([]contract.Bar, error)

const (
	holdDays    = 5      // «До отмены» живёт 5 торговых дней; позиция — 5 торговых дней после фила
	defaultTake = 2.0    // тейк «Открытый» — 2R
	costPerSide = 0.0005 // комиссия + проскальзывание, доля цены
	scanDays    = 25     // календарных дней вперёд — дальше не смотрим
)

type Outcome struct {
	Signal
	Status string // filled | expired | open | invalid | nodata
	Entry  float64
	Exit   float64
	Reason string // stop | take | time | contract
	R      float64
	At     time.Time
}

// Simulate исполняет карточку по правилу 0008. expireAt — когда ордер снят новой карточкой по
// тому же инструменту (нулевое — не снят). market — диагностика «вход по рынку в момент поста».
func Simulate(s Signal, bars Bars, expireAt time.Time, market bool, asOf time.Time) (Outcome, error) {
	o := Outcome{Signal: s}
	var all []contract.Bar
	day0 := time.Date(s.PostedAt.Year(), s.PostedAt.Month(), s.PostedAt.Day(), 0, 0, 0, 0, time.UTC)
	var tradingDays []time.Time
	// Окно: срок ордера (или ~8 календарных дней «До отмены») плюс 9 календарных на удержание.
	end := day0.AddDate(0, 0, 8)
	if !s.Until.IsZero() {
		end = s.Until
	}
	end = end.AddDate(0, 0, 9)
	if end.Sub(day0) > scanDays*24*time.Hour {
		end = day0.AddDate(0, 0, scanDays)
	}
	for d := day0; d.Before(end) && !d.After(asOf); d = d.AddDate(0, 0, 1) {
		b, err := bars(s.SecID, d)
		if err != nil {
			return o, err
		}
		if len(b) > 0 {
			tradingDays = append(tradingDays, d)
			all = append(all, b...)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Day.Before(all[j].Day) })
	i0 := sort.Search(len(all), func(i int) bool { return all[i].Day.After(s.PostedAt) })
	if i0 == len(all) {
		o.Status = "nodata"
		return o, nil
	}
	until := s.Until
	if until.IsZero() {
		until = endOfTradingDay(tradingDays, day0, holdDays)
	}
	if !expireAt.IsZero() && expireAt.Before(until) {
		until = expireAt
	}

	kind := s.Kind
	if market {
		kind = Market
	}
	fill := -1
	for i := i0; i < len(all) && all[i].Day.Before(until); i++ {
		b := all[i]
		switch kind {
		case Market:
			o.Entry, fill = b.Open, i
		case Limit:
			if s.Long && b.Low <= s.Price {
				o.Entry, fill = math.Min(s.Price, b.Open), i
			} else if !s.Long && b.High >= s.Price {
				o.Entry, fill = math.Max(s.Price, b.Open), i
			}
		case Stop:
			if s.Long && b.High >= s.Price {
				o.Entry, fill = math.Max(s.Price, b.Open), i
			} else if !s.Long && b.Low <= s.Price {
				o.Entry, fill = math.Min(s.Price, b.Open), i
			}
		}
		if fill >= 0 {
			break
		}
	}
	if fill < 0 {
		o.Status = "expired"
		return o, nil
	}
	sign := 1.0
	if !s.Long {
		sign = -1
	}
	risk := sign * (o.Entry - s.Stop)
	if risk <= 0 {
		o.Status = "invalid" // стоп по ту сторону входа — фил прошёл сквозь стоп или карточка с ошибкой
		return o, nil
	}
	take := s.Take
	if take == 0 || sign*(take-o.Entry) <= 0 {
		take = o.Entry + sign*defaultTake*risk
	}
	fillDay := time.Date(all[fill].Day.Year(), all[fill].Day.Month(), all[fill].Day.Day(), 0, 0, 0, 0, time.UTC)
	timeEnd := endOfTradingDay(tradingDays, fillDay, holdDays)

	exit := func(i int, px float64, why string) (Outcome, error) {
		o.Status, o.Exit, o.Reason, o.At = "filled", px, why, all[i].Day
		cost := costPerSide * (o.Entry + px) / risk
		o.R = sign*(px-o.Entry)/risk - cost
		return o, nil
	}
	for i := fill; i < len(all); i++ {
		b := all[i]
		if i > fill && !b.Day.Before(timeEnd) {
			return exit(i-1, all[i-1].Close, "time")
		}
		// стоп: гэп — по open, иначе по уровню; в свече фила — только стоп
		if s.Long && b.Low <= s.Stop {
			return exit(i, math.Min(s.Stop, openIf(i > fill, b.Open, s.Stop)), "stop")
		}
		if !s.Long && b.High >= s.Stop {
			return exit(i, math.Max(s.Stop, openIf(i > fill, b.Open, s.Stop)), "stop")
		}
		if i == fill {
			continue
		}
		if s.Long && b.High >= take {
			return exit(i, math.Max(take, b.Open), "take")
		}
		if !s.Long && b.Low <= take {
			return exit(i, math.Min(take, b.Open), "take")
		}
	}
	// данные кончились: контракт истёк — по последнему close; иначе позиция ещё открыта
	last := all[len(all)-1]
	if asOf.Sub(last.Day) > 72*time.Hour {
		return exit(len(all)-1, last.Close, "contract")
	}
	o.Status = "open"
	return o, nil
}

// openIf: на гэпе стоп исполняется по open (хуже уровня); в свече фила гэпа нет — вход в ней.
func openIf(gap bool, open, level float64) float64 {
	if gap {
		return open
	}
	return level
}

// endOfTradingDay — начало торгового дня, следующего за n-м торговым днём от from (не включая from).
func endOfTradingDay(days []time.Time, from time.Time, n int) time.Time {
	k := 0
	for _, d := range days {
		if d.After(from) {
			k++
			if k == n {
				return d.Add(24 * time.Hour)
			}
		}
	}
	if len(days) > 0 {
		return days[len(days)-1].Add(24 * time.Hour)
	}
	return from.Add(time.Duration(n) * 24 * time.Hour)
}

// Stat — сводка по исполненным сделкам.
type Stat struct {
	N, Weeks     int
	Mean, Median float64
	Win          float64
	TWeeks       float64
}

func Summarize(os []Outcome) Stat {
	var s Stat
	var rs []float64
	week := map[[2]int][]float64{}
	for _, o := range os {
		if o.Status != "filled" {
			continue
		}
		rs = append(rs, o.R)
		y, w := o.PostedAt.ISOWeek()
		week[[2]int{y, w}] = append(week[[2]int{y, w}], o.R)
	}
	s.N = len(rs)
	if s.N == 0 {
		return s
	}
	var win int
	for _, r := range rs {
		s.Mean += r
		if r > 0 {
			win++
		}
	}
	s.Mean /= float64(s.N)
	s.Win = float64(win) / float64(s.N)
	sort.Float64s(rs)
	s.Median = rs[len(rs)/2]
	var wm []float64
	for _, v := range week {
		var m float64
		for _, x := range v {
			m += x
		}
		wm = append(wm, m/float64(len(v)))
	}
	s.Weeks = len(wm)
	if len(wm) > 1 {
		var mean, v float64
		for _, x := range wm {
			mean += x
		}
		mean /= float64(len(wm))
		for _, x := range wm {
			v += (x - mean) * (x - mean)
		}
		if sd := math.Sqrt(v / float64(len(wm)-1)); sd > 0 {
			s.TWeeks = mean / (sd / math.Sqrt(float64(len(wm))))
		}
	}
	return s
}
