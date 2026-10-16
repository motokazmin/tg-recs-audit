// Package portfolio — виртуальный портфель, который строго следует идеям канала.
//
// Один движок и для бэктеста, и для «живого» режима: портфель — это прогон всех
// идей по порядку по дневным ценам. Новые посты просто удлиняют прогон.
//
// Правила (зафиксированы до первого прогона, docs/adr/0002):
//   - вход — по open первого торгового дня ПОСЛЕ дня поста (по Москве); пост в 09:03
//     не успевает к open того же дня: дневной бар ISS открывается утренней сессией 07:00;
//   - сумма на идею фиксирована, акций — целое число, минимум одна;
//   - выход — по цели (лимит: по цели, на гэпе — по open) или по горизонту:
//     year-end — close последнего торгового дня года входа, 12m — через год после входа;
//   - пока бумага в портфеле, новые идеи по ней пропускаются (с записью);
//   - «Держать»/«Продавать» — не покупка, пропуск.
package portfolio

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/feature/ideas"
)

// Horizon — когда продавать, если цель не достигнута: "year-end" — close последнего
// торгового дня года входа, "<N>m" — последнего торгового дня не позже входа + N месяцев.
type Horizon string

const YearEnd Horizon = "year-end"

var monthsRe = regexp.MustCompile(`^([1-9][0-9]?)m$`)

// ParseHorizon проверяет вид горизонта.
func ParseHorizon(s string) (Horizon, error) {
	if s == string(YearEnd) || monthsRe.MatchString(s) {
		return Horizon(s), nil
	}
	return "", fmt.Errorf("горизонт %q: year-end или <N>m (1m, 12m)", s)
}

func (h Horizon) deadline(entry time.Time) time.Time {
	if m := monthsRe.FindStringSubmatch(string(h)); m != nil {
		n, _ := strconv.Atoi(m[1])
		return entry.AddDate(0, n, 0)
	}
	return time.Date(entry.Year(), 12, 31, 0, 0, 0, 0, time.UTC)
}

type Params struct {
	PerIdea       float64 // рублей на идею
	Horizon       Horizon
	CommissionBps float64 // на каждую ногу
	// Stack — каждая идея отдельной позицией, даже если бумага уже в портфеле.
	// Проверка устойчивости: основное правило — не докупать (72% идей ruforecasts — повторы).
	Stack bool
}

var msk = time.FixedZone("MSK", 3*3600)

const (
	jumpRatio  = 3.0 // close за день изменился больше чем втрое — сплит, без записи в таблице это ошибка
	staleDays  = 14  // столько дней без торгов к asOf — бумага не торгуется
	maxWaitDay = 45  // от поста до первого торгового дня дольше — бумага не торговалась (кроме остановки 2022)
)

// Position — путь одной идеи от входа до выхода.
type Position struct {
	Idea       ideas.Idea
	SecID      string // текущий код: после переименования отличается от Idea.SecID
	EntryDay   time.Time
	EntryPrice float64
	Qty        float64 // в акциях на момент входа
	Cost       float64 // руб. с комиссией
	ExitDay    time.Time
	ExitPrice  float64 // в ценах текущего кода
	ExitQty    float64 // акций на выходе: после сплитов и обменов не равно Qty
	Proceeds   float64 // руб. после комиссии; у открытой — оценка по последнему close
	ExitReason string  // «цель», «цель (гэп)», «горизонт», «торги прекращены»; пусто — открыта
	Dividends  []Dividend
	BenchIn    float64 // бенчмарк на входе и на выходе — зеркальная покупка индекса
	BenchOut   float64
}

// Dividend — начисленная выплата: держали на закрытии LastBuyDay и не продали в этот день.
type Dividend struct {
	LastBuyDay time.Time
	SecID      string
	Qty        float64 // акций на тот момент
	PerShare   float64 // в фактическом масштабе на тот момент (не пересчитанном под поздние сплиты)
}

func (d Dividend) Amount() float64 { return d.Qty * d.PerShare }

func (p Position) DividendSum() float64 {
	var s float64
	for _, d := range p.Dividends {
		s += d.Amount()
	}
	return s
}

func (p Position) Open() bool        { return p.ExitReason == "" }
func (p Position) Return() float64   { return (p.Proceeds+p.DividendSum())/p.Cost - 1 }
func (p Position) Bench() float64    { return p.BenchOut/p.BenchIn - 1 }
func (p Position) HoldDays() float64 { return p.ExitDay.Sub(p.EntryDay).Hours() / 24 }

// Skip — идея, по которой покупки не было, и почему.
type Skip struct {
	Idea   ideas.Idea
	Day    time.Time
	Reason string
}

// Result — итог прогона.
type Result struct {
	Positions []Position
	Skips     []Skip
	Pending   []ideas.Idea // пост есть, торгового дня после него ещё нет
	Problems  []string     // нужна запись в таблице корпоративных действий — прогон недостоверен
	AsOf      time.Time
}

type engine struct {
	p        Params
	prices   contract.PriceHistory
	divSrc   contract.DividendSource // nil — без дивидендов
	divs     map[string][]credit
	divFixes []contract.DividendFix
	bench    map[string]contract.Bar
	splits   map[string]map[string]float64 // secid → день → множитель
	renames  map[string]contract.Rename
	frozen   map[string]bool
	asOf     time.Time
	cache    map[string][]contract.Bar
}

// Run прогоняет идеи по порядку публикации. bench — дневные бары бенчмарка
// (с дивидендами — индекс полной доходности MCFTR, без — IMOEX); divs == nil — без дивидендов.
func Run(all []ideas.Idea, prices contract.PriceHistory, divs contract.DividendSource, bench []contract.Bar, ca contract.CorpActions, p Params, asOf time.Time) (Result, error) {
	e := &engine{
		p: p, prices: prices, divSrc: divs, divs: map[string][]credit{}, bench: map[string]contract.Bar{}, splits: map[string]map[string]float64{},
		renames: map[string]contract.Rename{}, frozen: map[string]bool{}, asOf: asOf, cache: map[string][]contract.Bar{},
	}
	for _, b := range bench {
		e.bench[key(b.Day)] = b
	}
	for _, s := range ca.Splits {
		if e.splits[s.SecID] == nil {
			e.splits[s.SecID] = map[string]float64{}
		}
		e.splits[s.SecID][s.Day] = s.Factor
	}
	for _, r := range ca.Renames {
		e.renames[r.From] = r
	}
	for _, f := range ca.Frozen {
		e.frozen[f.SecID] = true
	}
	e.divFixes = ca.Dividends

	sorted := append([]ideas.Idea(nil), all...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].PostedAt.Before(sorted[j].PostedAt) })

	res := Result{AsOf: asOf}
	busyUntil := map[string]*Position{} // по коду из идеи: последняя позиция
	problems := map[string]bool{}
	for _, idea := range sorted {
		if idea.Rating == "Держать" || idea.Rating == "Продавать" {
			res.Skips = append(res.Skips, Skip{idea, postDay(idea), "рейтинг «" + idea.Rating + "»"})
			continue
		}
		bars, err := e.bars(idea.SecID)
		if err != nil {
			return res, err
		}
		if len(bars) == 0 {
			res.Skips = append(res.Skips, Skip{idea, postDay(idea), "нет истории на MOEX"})
			continue
		}
		i := sort.Search(len(bars), func(i int) bool { return bars[i].Day.After(postDay(idea)) })
		if i == len(bars) {
			if len(bars) > 0 && asOf.Sub(bars[len(bars)-1].Day) > staleDays*24*time.Hour {
				res.Skips = append(res.Skips, Skip{idea, postDay(idea), "бумага не торгуется на дату поста"})
			} else {
				res.Pending = append(res.Pending, idea)
			}
			continue
		}
		entry := bars[i]
		if wait := entry.Day.Sub(postDay(idea)).Hours() / 24; wait > maxWaitDay && !inHalt(postDay(idea)) {
			res.Skips = append(res.Skips, Skip{idea, entry.Day, fmt.Sprintf("торги возобновились только через %.0f дн.", wait)})
			continue
		}
		if prev := busyUntil[idea.SecID]; !e.p.Stack && prev != nil && (prev.Open() || !prev.ExitDay.Before(entry.Day)) {
			res.Skips = append(res.Skips, Skip{idea, entry.Day, fmt.Sprintf("уже в портфеле (пост %d)", prev.Idea.PostID)})
			continue
		}
		if entry.Open >= idea.Target {
			res.Skips = append(res.Skips, Skip{idea, entry.Day, "цель достигнута до входа"})
			continue
		}
		pos, problem, err := e.hold(idea, bars, i)
		if err != nil {
			return res, err
		}
		if problem != "" {
			if !problems[problem] {
				problems[problem] = true
				res.Problems = append(res.Problems, problem)
			}
			continue
		}
		res.Positions = append(res.Positions, pos)
		busyUntil[idea.SecID] = &res.Positions[len(res.Positions)-1]
	}
	return res, nil
}

// hold ведёт позицию от бара входа до выхода или до конца данных.
func (e *engine) hold(idea ideas.Idea, bars []contract.Bar, i int) (Position, string, error) {
	entry := bars[i]
	qty := math.Max(1, math.Floor(e.p.PerIdea/entry.Open))
	comm := e.p.CommissionBps / 1e4
	pos := Position{
		Idea: idea, SecID: idea.SecID, EntryDay: entry.Day, EntryPrice: entry.Open, Qty: qty,
		Cost: qty * entry.Open * (1 + comm), BenchIn: e.benchAt(entry.Day, true),
	}
	deadline := e.p.Horizon.deadline(entry.Day)
	target, shares := idea.Target, qty
	exit := func(day time.Time, price float64, reason string) (Position, string, error) {
		pos.ExitDay, pos.ExitPrice, pos.ExitQty, pos.ExitReason = day, price, shares, reason
		pos.Proceeds = shares * price * (1 - comm)
		if reason == "" {
			pos.Proceeds = shares * price // открытая — без комиссии продажи
		}
		pos.BenchOut = e.benchAt(day, false)
		return pos, "", nil
	}

	code, seg := idea.SecID, i               // seg — первый бар текущего кода: до него сравнивать не с чем
	paidUntil := entry.Day.AddDate(0, 0, -1) // выплаты с T-1 до дня входа — не наши
	// pay начисляет выплаты code с T-1 в (paidUntil, upTo]: позиция пережила закрытие upTo.
	pay := func(upTo time.Time) (string, error) {
		cs, err := e.dividends(code)
		if err != nil {
			return "", err
		}
		for _, c := range cs {
			if !c.lastBuy.After(paidUntil) || c.lastBuy.After(upTo) {
				continue
			}
			if problem, err := e.checkScale(code, c); problem != "" || err != nil {
				return problem, err
			}
			pos.Dividends = append(pos.Dividends, Dividend{c.lastBuy, code, shares, c.perShare})
		}
		if upTo.After(paidUntil) {
			paidUntil = upTo
		}
		return "", nil
	}
	for j := i; ; j++ {
		if j == len(bars) {
			last := bars[j-1]
			stale := e.asOf.Sub(last.Day) > staleDays*24*time.Hour
			if !stale {
				if problem, err := pay(last.Day); problem != "" || err != nil {
					return pos, problem, err
				}
				return exit(last.Day, last.Close, "") // открыта: оценка по последнему close
			}
			if r, ok := e.renames[code]; ok {
				// Пережили закрытие последнего бара старого кода (не продали) — его выплаты наши.
				if problem, err := pay(last.Day); problem != "" || err != nil {
					return pos, problem, err
				}
				next, err := e.bars(r.To)
				if err != nil {
					return pos, "", err
				}
				k := sort.Search(len(next), func(k int) bool { return next[k].Day.After(last.Day) })
				if k == len(next) {
					return pos, fmt.Sprintf("%s → %s: у преемника нет торгов после %s", code, r.To, key(last.Day)), nil
				}
				shares *= r.Ratio
				target /= r.Ratio
				code, pos.SecID, bars, j, seg = r.To, r.To, next, k, k
			} else if e.frozen[code] {
				return exit(last.Day, last.Close, "торги прекращены")
			} else {
				return pos, fmt.Sprintf("%s: торгов нет с %s — нужна запись rename или frozen", code, key(last.Day)), nil
			}
		}
		b := bars[j]
		if j > seg && b.Day.After(deadline) {
			prev := bars[j-1]
			return exit(prev.Day, prev.Close, "горизонт")
		}
		if j == seg && j != i && b.Day.After(deadline) {
			// Горизонт прошёл, пока бумага не торговалась: продать можно только в первый день нового кода.
			return exit(b.Day, b.Open, "горизонт")
		}
		if j > seg {
			// Не продали на закрытии предыдущего бара — выплаты с T-1 по него наши.
			if problem, err := pay(bars[j-1].Day); problem != "" || err != nil {
				return pos, problem, err
			}
			ratio := b.Close / bars[j-1].Close
			if f, ok := e.splits[code][key(b.Day)]; ok {
				shares *= f
				target /= f
				// Бар дня сплита бывает смешанным: у BELU 2024-08-22 open/high 4223 в старом
				// масштабе, low/close ~700 в новом. Верим только close.
				if b.Close >= target {
					return exit(b.Day, b.Close, "цель")
				}
				continue
			} else if ratio > jumpRatio || ratio < 1/jumpRatio {
				return pos, fmt.Sprintf("%s: close %s → %s ×%.4g — сплит? нужна запись в splits", code, key(bars[j-1].Day), key(b.Day), ratio), nil
			}
		}
		if j != i && b.Open >= target {
			return exit(b.Day, b.Open, "цель (гэп)")
		}
		if b.High >= target {
			return exit(b.Day, target, "цель")
		}
	}
}

func (e *engine) bars(secid string) ([]contract.Bar, error) {
	if b, ok := e.cache[secid]; ok {
		return b, nil
	}
	b, err := e.prices.DailyBars(secid)
	if err != nil {
		return nil, err
	}
	// asOf отсекает будущее: прогон «на дату» не видит баров после неё
	cut := sort.Search(len(b), func(i int) bool { return b[i].Day.After(e.asOf) })
	b = b[:cut]
	e.cache[secid] = b
	return b, nil
}

// benchAt — бенчмарк на open дня входа или close дня выхода; нет бара — ближайший предыдущий close.
func (e *engine) benchAt(day time.Time, open bool) float64 {
	for d := day; day.Sub(d) < 30*24*time.Hour; d = d.AddDate(0, 0, -1) {
		if b, ok := e.bench[key(d)]; ok {
			if open && d.Equal(day) && b.Open > 0 {
				return b.Open
			}
			return b.Close
		}
	}
	return math.NaN()
}

func postDay(i ideas.Idea) time.Time {
	t := i.PostedAt.In(msk)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Остановка торгов акциями 2022-02-28 … 2022-03-23: ожидание входа дольше обычного законно.
func inHalt(d time.Time) bool {
	return !d.Before(time.Date(2022, 2, 20, 0, 0, 0, 0, time.UTC)) && d.Before(time.Date(2022, 3, 31, 0, 0, 0, 0, time.UTC))
}

func key(t time.Time) string { return t.Format(time.DateOnly) }
