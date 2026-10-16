// Package divevents — «купить после рекомендации дивидендов, держать до отсечки» (регистрация 0010).
// Даты объявлений — из канала (t.me/divonline), цены — дневные бары ISS, бенчмарк — MCFTR.
package divevents

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"tg-recs-audit/internal/contract"
)

type Event struct {
	PostID   int64
	PostedAt time.Time
	SecID    string
	Div      float64 // руб. на акцию из поста
	Yield    float64 // % из поста
	LastBuy  time.Time
	Interim  bool // промежуточная выплата (кв., полугодие, 9 мес.)
}

type Unparsed struct {
	PostID int64
	Reason string
}

var (
	cashOrTag = regexp.MustCompile(`(?:#|MOEX:)([A-Z][A-Z0-9]{2,5})\b`)
	divRe     = regexp.MustCompile(`:\s*(?:[\d,.\s+]*=\s*)?(\d+(?:[.,]\d+)?)`)
	yieldRe   = regexp.MustCompile(`(?i)доходность[^:\n]*:\s*(\d+(?:[.,]\d+)?)\s*%`)
	lastRe    = regexp.MustCompile(`(?i)Последний день покупки:\s*([^\n]+)`)
	numDate   = regexp.MustCompile(`(\d{1,2})\.(\d{1,2})\.(\d{4})`)
	wordDate  = regexp.MustCompile(`(\d{1,2})\s+([а-я]+)\s+(\d{4})`)
	interimRe = regexp.MustCompile(`(?i)(кв\.|квартал|полугоди|9 мес|6 мес|3 мес)`) // без \b: в Go он только для ASCII
)

var monthsRu = map[string]time.Month{"января": 1, "февраля": 2, "марта": 3, "апреля": 4, "мая": 5, "июня": 6,
	"июля": 7, "августа": 8, "сентября": 9, "октября": 10, "ноября": 11, "декабря": 12}

// Parse — посты «Рекомендованы дивиденды». known — тикеры акций MOEX. Повтор той же выплаты
// (тикер + T-1) — берётся первая рекомендация.
func Parse(msgs []contract.Message, known map[string]bool) ([]Event, []Unparsed) {
	var out []Event
	var un []Unparsed
	seen := map[string]bool{}
	for _, m := range msgs {
		if !strings.Contains(m.Text, "Рекомендованы дивиденды") {
			continue
		}
		e := Event{PostID: m.ID, PostedAt: m.PostedAt, Interim: interimRe.MatchString(strings.SplitN(m.Text, "\n", 2)[0])}
		// Тикер: обыкновенная акция, если в посте есть и она, и префы (дивиденд в посте — по ней).
		var cands []string
		for _, t := range cashOrTag.FindAllStringSubmatch(m.Text, -1) {
			if known[t[1]] {
				cands = append(cands, t[1])
			}
		}
		for _, t := range m.Tags {
			if s := strings.ToUpper(t); known[s] {
				cands = append(cands, s)
			}
		}
		if len(cands) == 0 {
			un = append(un, Unparsed{m.ID, "нет тикера"})
			continue
		}
		sort.Slice(cands, func(i, j int) bool { return len(cands[i]) < len(cands[j]) })
		e.SecID = cands[0]
		lb := lastRe.FindStringSubmatch(m.Text)
		if lb == nil {
			un = append(un, Unparsed{m.ID, "нет последнего дня покупки"})
			continue
		}
		d, ok := parseDate(lb[1])
		if !ok {
			un = append(un, Unparsed{m.ID, "последний день покупки не дата: " + strings.TrimSpace(lb[1])})
			continue
		}
		e.LastBuy = d
		e.Div = dividend(m.Text)
		if e.Div <= 0 {
			un = append(un, Unparsed{m.ID, "нет суммы дивиденда"})
			continue
		}
		if ym := yieldRe.FindStringSubmatch(m.Text); ym != nil {
			e.Yield = num(ym[1])
		}
		k := e.SecID + e.LastBuy.Format(time.DateOnly)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	return out, un
}

// dividend — сумма из первой строки со словом «дивиденд», кроме заголовка и доходности:
// «Дивиденд на акцию: 25,43», «Финальный дивиденд: 28», «Суммарный дивиденд: 22,51+22,92= 45,43».
func dividend(text string) float64 {
	for _, l := range strings.Split(text, "\n") {
		low := strings.ToLower(l)
		if !strings.Contains(low, "дивиденд") || strings.Contains(low, "доходн") || strings.Contains(low, "рекомендованы") {
			continue
		}
		if m := divRe.FindStringSubmatch(l); m != nil {
			return num(m[1])
		}
	}
	return 0
}

func parseDate(s string) (time.Time, bool) {
	if m := numDate.FindStringSubmatch(s); m != nil {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		return time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC), true
	}
	if m := wordDate.FindStringSubmatch(strings.ToLower(s)); m != nil {
		if mo, ok := monthsRu[m[2]]; ok {
			d, _ := strconv.Atoi(m[1])
			y, _ := strconv.Atoi(m[3])
			return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC), true
		}
	}
	return time.Time{}, false
}

func num(s string) float64 {
	v, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	return v
}

// Result — доходность события против MCFTR на двух выходах.
type Result struct {
	Event
	Skip      string // ≠ "" — событие не исполнимо
	EntryDay  time.Time
	RetB, MCB float64 // через отсечку: close дня отсечки + дивиденд
	RetA, MCA float64 // разгон: close дня T-1
	Days      int     // торговых дней от входа до T-1
	Paid      bool    // выплата подтверждена источником выплат; нет — дивиденд не начисляется
	Tax       float64 // НДФЛ 13% с дивиденда, доля цены входа (для варианта «после налога»)
}

const cost = 0.0005 // на ногу

var msk = time.FixedZone("MSK", 3*3600)

// Evaluate считает событие по дневным барам бумаги и MCFTR. paid — выплата подтверждена:
// рекомендацию совета может отменить собрание (2022), тогда дивиденда нет, а цена его уже ждала.
func Evaluate(e Event, bars, mcftr []contract.Bar, paid bool) Result {
	r := Result{Event: e, Paid: paid}
	if !paid {
		e.Div = 0
	}
	t := e.PostedAt.In(msk)
	post := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	i := sort.Search(len(bars), func(i int) bool { return bars[i].Day.After(post) })
	if i == len(bars) {
		r.Skip = "нет торгов после поста"
		return r
	}
	if bars[i].Day.After(e.LastBuy) {
		r.Skip = "T-1 раньше входа"
		return r
	}
	a := sort.Search(len(bars), func(k int) bool { return bars[k].Day.After(e.LastBuy) }) - 1 // close T-1
	b := a + 1                                                                                // день отсечки
	if b >= len(bars) {
		r.Skip = "отсечка ещё не прошла"
		return r
	}
	for k := i + 1; k <= b; k++ {
		if q := bars[k].Close / bars[k-1].Close; q > 3 || q < 1.0/3 {
			r.Skip = "скачок цены (сплит?) в окне"
			return r
		}
	}
	r.EntryDay, r.Days = bars[i].Day, a-i
	in := bars[i].Open * (1 + cost)
	r.RetA = bars[a].Close*(1-cost)/in - 1
	r.RetB = (bars[b].Close*(1-cost)+e.Div)/in - 1
	r.Tax = 0.13 * e.Div / in
	r.MCA = idx(mcftr, bars[a].Day, false)/idx(mcftr, bars[i].Day, true) - 1
	r.MCB = idx(mcftr, bars[b].Day, false)/idx(mcftr, bars[i].Day, true) - 1
	if math.IsNaN(r.MCA) || math.IsNaN(r.MCB) {
		r.Skip = "нет MCFTR"
	}
	return r
}

func idx(bars []contract.Bar, day time.Time, open bool) float64 {
	k := sort.Search(len(bars), func(i int) bool { return bars[i].Day.After(day) }) - 1
	if k < 0 {
		return math.NaN()
	}
	if open && bars[k].Day.Equal(day) && bars[k].Open > 0 {
		return bars[k].Open
	}
	return bars[k].Close
}

// Stat — превышение над MCFTR по событиям.
type Stat struct {
	N, Weeks     int
	Mean, Median float64
	Pos, TWeeks  float64
}

func Summarize(rs []Result, ex func(Result) float64) Stat {
	var s Stat
	var xs []float64
	week := map[[2]int][]float64{}
	for _, r := range rs {
		if r.Skip != "" {
			continue
		}
		x := ex(r)
		xs = append(xs, x)
		y, w := r.EntryDay.ISOWeek()
		week[[2]int{y, w}] = append(week[[2]int{y, w}], x)
	}
	s.N = len(xs)
	if s.N == 0 {
		return s
	}
	var pos int
	for _, x := range xs {
		s.Mean += x
		if x > 0 {
			pos++
		}
	}
	s.Mean /= float64(s.N)
	s.Pos = float64(pos) / float64(s.N)
	sort.Float64s(xs)
	s.Median = xs[len(xs)/2]
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
