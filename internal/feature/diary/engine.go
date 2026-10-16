package diary

import (
	"fmt"
	"math"
	"sort"
	"time"

	"tg-recs-audit/internal/contract"
)

// Mode — по какой цене исполняем сделки автора.
type Mode string

const (
	// Author — день поста, сумма из поста: восстановленный портфель самого автора.
	Author Mode = "author"
	// Follower — open первой сессии после поста: так мог бы повторить подписчик.
	Follower Mode = "follower"
)

type Params struct {
	Mode          Mode
	CommissionBps float64 // последователю на каждую сделку; у автора — его сумма как есть
	DebtSpread    float64 // долг стоит LQDT + спред, годовых (0.04 = 4%)
}

type Deps struct {
	Resolver    contract.InstrumentResolver
	Shares      contract.PriceHistory // акции и фонды
	Bonds       contract.BondData
	Divs        contract.DividendSource // nil — без дивидендов
	FX          contract.FXRates        // курсы для облигаций с номиналом не в рублях
	CA          contract.CorpActions
	MoneyMarket []contract.Bar // LQDT: дневной прирост — цена денег и справочный бенчмарк
	Bench       []contract.Bar // MCFTR: календарь и бенчмарк
}

// Op — движение по счёту.
type Op struct {
	Day    time.Time
	Kind   string // contribution | buy | sell | coupon | amortization | dividend | interest
	SecID  string
	Qty    float64
	Price  float64 // руб. за бумагу (у облигаций — с НКД)
	Amount float64 // руб.: + приход на счёт, − расход
	PostID int64
	Note   string
}

type Point struct {
	Day         time.Time
	Cash        float64
	Holdings    float64
	Contributed float64
	Bench       float64 // те же взносы в те же дни в бенчмарк
	MoneyMarket float64 // …и в фонд денежного рынка: что дали бы деньги без риска
}

func (p Point) Equity() float64 { return p.Cash + p.Holdings }

// Check — позиция автора из поста против нашей.
type Check struct {
	PostID      int64
	Day         time.Time
	SecID       string
	Author, Our float64
}

// Diff — состав портфеля из #БалансПортфеля против нашего на тот же день.
type Diff struct {
	PostID  int64
	Day     time.Time
	Missing []string // у автора есть, у нас нет — пропущена покупка
	Extra   []string // у нас есть, у автора нет — пропущена продажа
}

type Result struct {
	Ops      []Op
	Curve    []Point
	Checks   []Check
	Diffs    []Diff
	Problems []string           // сделка не исполнена: бумага не найдена, рынка нет, сумма не сходится ни с чем
	Warnings []string           // исполнена с допущением: количество из суммы, продажа урезана до позиции
	Holdings map[string]float64 // на конец
}

const (
	shareTol = 0.03 // цена из суммы автора — в диапазоне дня ±3% (в сумме бывает комиссия)
	bondTol  = 0.02 // у облигаций ±2% (НКД в сумме может быть, а может нет)
)

var msk = time.FixedZone("MSK", 3*3600)

func dayOf(t time.Time) time.Time {
	t = t.In(msk)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

type sim struct {
	p     Params
	d     Deps
	res   Result
	inst  map[string]contract.Instrument // ref → бумага
	share map[string][]contract.Bar
	bond  map[string][]contract.BondBar
	flows map[string][]contract.BondFlow
	divs  map[string][]divRow
	split map[string]map[string]float64
	fx    map[string][]contract.Rate // валюта номинала → курсы ЦБ
	qty   map[string]float64         // secid → штук
	cash  float64
}

type divRow struct {
	lastBuy  time.Time
	perShare float64
}

// Run — счёт автора с первого взноса до последнего бара бенчмарка.
func Run(cs []Contribution, ts []Trade, hs []Holding, ss []Snapshot, p Params, d Deps) (Result, error) {
	s := &sim{p: p, d: d, inst: map[string]contract.Instrument{}, share: map[string][]contract.Bar{},
		bond: map[string][]contract.BondBar{}, flows: map[string][]contract.BondFlow{}, divs: map[string][]divRow{},
		split: map[string]map[string]float64{}, fx: map[string][]contract.Rate{}, qty: map[string]float64{}}
	for _, sp := range d.CA.Splits {
		if s.split[sp.SecID] == nil {
			s.split[sp.SecID] = map[string]float64{}
		}
		s.split[sp.SecID][sp.Day] = sp.Factor
	}
	if len(d.Bench) == 0 || len(cs) == 0 {
		return s.res, fmt.Errorf("нет бенчмарка или взносов")
	}
	cal := tradingDays(d.Bench, dayOf(cs[0].PostedAt))
	asOf := cal[len(cal)-1]

	// Резолвим бумаги заранее: ошибка резолва — проблема, а не падение.
	for i := range ts {
		if _, err := s.instrument(ts[i].Ref); err != nil {
			s.problem("пост %d: %v", ts[i].PostID, err)
		}
	}

	// Количество и цену — один раз для обеих моделей.
	ts = append([]Trade(nil), ts...)
	valid := make([]bool, len(ts))
	for i := range ts {
		if in, ok := s.inst[ts[i].Ref]; ok {
			valid[i] = s.resolve(&ts[i], in)
		}
	}

	// События по дням исполнения.
	contribOn := map[time.Time][]Contribution{}
	for _, c := range cs {
		contribOn[firstOnOrAfter(cal, dayOf(c.PostedAt))] = append(contribOn[firstOnOrAfter(cal, dayOf(c.PostedAt))], c)
	}
	tradesOn := map[time.Time][]Trade{}
	for i, t := range ts {
		in, ok := s.inst[t.Ref]
		if !ok || !valid[i] {
			continue
		}
		day, err := s.execDay(t, in, cal)
		if err != nil {
			s.problem("пост %d %s: %v", t.PostID, in.SecID, err)
			continue
		}
		if day.After(asOf) {
			continue // исполнится в следующей сессии
		}
		tradesOn[day] = append(tradesOn[day], t)
	}
	snapOn := map[time.Time][]Snapshot{}
	for _, sn := range ss {
		day := firstOnOrAfter(cal, dayOf(sn.PostedAt))
		snapOn[day] = append(snapOn[day], sn)
	}
	checksAfter := map[int64][]Holding{}
	for _, h := range hs {
		checksAfter[h.PostID] = append(checksAfter[h.PostID], h)
	}

	var contributed, benchUnits, mmUnits float64
	benchBy := map[time.Time]contract.Bar{}
	for _, b := range d.Bench {
		benchBy[b.Day] = b
	}
	mm := map[time.Time]float64{}
	for _, b := range d.MoneyMarket {
		mm[b.Day] = b.Close
	}

	for i, day := range cal {
		// 1. Долг за ночь: прирост LQDT + спред.
		if i > 0 && s.cash < 0 {
			prev := cal[i-1]
			r := 0.0
			if a, b := mm[prev], mm[day]; a > 0 && b > 0 {
				r = b/a - 1
			}
			r += s.p.DebtSpread * day.Sub(prev).Hours() / 24 / 365
			s.op(Op{Day: day, Kind: "interest", Amount: s.cash * r, Note: "долг под LQDT + спред"})
		}
		// 2. Сплиты и смены тикеров: число и код акций меняются до торгов.
		for _, secid := range s.held() {
			q := s.qty[secid]
			if f, ok := s.split[secid][key(day)]; ok && q > 0 {
				s.qty[secid] = q * f
			}
		}
		for _, r := range s.d.CA.Renames {
			if q := s.qty[r.From]; q > 0 && s.liveCode(r.From, day) != r.From {
				s.qty[r.To] += q * r.Ratio
				s.qty[r.From] = 0
				s.op(Op{Day: day, Kind: "exchange", SecID: r.To, Qty: q * r.Ratio, Note: r.From + " → " + r.To})
			}
		}
		// 3. Выплаты по облигациям (держали на конец предыдущего торгового дня).
		if i > 0 {
			s.bondFlows(cal[i-1], day)
		}
		// 4. Взносы.
		for _, c := range contribOn[day] {
			contributed += c.Amount
			if b, ok := benchBy[day]; ok && b.Close > 0 {
				benchUnits += c.Amount / b.Close
			}
			if m := mm[day]; m > 0 {
				mmUnits += c.Amount / m
			}
			s.op(Op{Day: day, Kind: "contribution", Amount: c.Amount, PostID: c.PostID})
		}
		// 5. Сделки в порядке постов; сверка позиции — после сделок своего поста.
		tt := tradesOn[day]
		sort.SliceStable(tt, func(a, b int) bool { return tt[a].PostedAt.Before(tt[b].PostedAt) })
		for k, t := range tt {
			s.trade(t, day)
			if k == len(tt)-1 || tt[k+1].PostID != t.PostID {
				if s.p.Mode == Author {
					for _, h := range checksAfter[t.PostID] {
						if in, ok := s.inst[h.Ref]; ok {
							s.res.Checks = append(s.res.Checks, Check{h.PostID, day, in.SecID, h.Qty, s.qty[in.SecID]})
						}
					}
				}
			}
		}
		// 6. Дивиденды: держим на закрытии T-1 после сделок дня.
		s.dividends(day)
		if s.p.Mode == Author {
			for _, sn := range snapOn[day] {
				s.compare(sn, day)
			}
		}
		// 7. Оценка.
		pt := Point{Day: day, Cash: s.cash, Contributed: contributed}
		for _, secid := range s.held() {
			q := s.qty[secid]
			if q > 0 {
				pt.Holdings += q * s.markPrice(secid, day)
			}
		}
		if b, ok := benchBy[day]; ok {
			pt.Bench = benchUnits * b.Close
		}
		pt.MoneyMarket = mmUnits * mm[day]
		s.res.Curve = append(s.res.Curve, pt)
	}
	s.res.Holdings = map[string]float64{}
	for k, v := range s.qty {
		if v > 1e-9 {
			s.res.Holdings[k] = v
		}
	}
	return s.res, nil
}

// compare — наш состав против списка $тикеров автора. Мелочь (< 0.5% капитала) не считаем:
// автор перечисляет не всё, а остатки в пару штук сравнение только засоряют.
func (s *sim) compare(sn Snapshot, day time.Time) {
	theirs := map[string]bool{}
	for _, r := range sn.Refs {
		in, err := s.instrument(r)
		if err != nil {
			continue
		}
		code := in.SecID
		if in.Kind != "bond" {
			code = s.liveCode(code, day)
		}
		theirs[code] = true
	}
	total := s.cash
	for _, secid := range s.held() {
		q := s.qty[secid]
		total += q * s.markPrice(secid, day)
	}
	d := Diff{PostID: sn.PostID, Day: day}
	for code := range theirs {
		if s.qty[code] <= 0 {
			d.Missing = append(d.Missing, code)
		}
	}
	for _, secid := range s.held() {
		q := s.qty[secid]
		if q > 0 && !theirs[secid] && q*s.markPrice(secid, day) > 0.005*total {
			d.Extra = append(d.Extra, secid)
		}
	}
	sort.Strings(d.Missing)
	sort.Strings(d.Extra)
	if len(d.Missing)+len(d.Extra) > 0 {
		s.res.Diffs = append(s.res.Diffs, d)
	}
}

// LedgerOps — операции счёта для журнала: только дни строго до cutoff (незакрытый день не фиксируем).
func LedgerOps(r Result, cutoff time.Time) []contract.Op {
	var out []contract.Op
	for _, o := range r.Ops {
		if !o.Day.Before(cutoff) {
			continue
		}
		out = append(out, contract.Op{
			Seq: len(out) + 1, Kind: o.Kind, Day: key(o.Day), SecID: o.SecID, Qty: o.Qty,
			Price: math.Round(o.Price*1e6) / 1e6, Amount: math.Round(o.Amount*100) / 100, PostID: o.PostID, Reason: o.Note,
		})
	}
	return out
}

// held — коды с позицией по порядку: обход map в Go случаен, а от порядка зависят
// порядок операций внутри дня и копейки округления — журнал перестал бы сверяться.
func (s *sim) held() []string {
	keys := make([]string, 0, len(s.qty))
	for k := range s.qty {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (s *sim) problem(f string, a ...any) {
	msg := fmt.Sprintf(f, a...)
	for _, p := range s.res.Problems {
		if p == msg {
			return // ошибка выплат повторялась бы каждый день
		}
	}
	s.res.Problems = append(s.res.Problems, msg)
}

func (s *sim) warn(f string, a ...any) {
	s.res.Warnings = append(s.res.Warnings, fmt.Sprintf(f, a...))
}

func (s *sim) op(o Op) {
	s.cash += o.Amount
	s.res.Ops = append(s.res.Ops, o)
}

func (s *sim) instrument(ref string) (contract.Instrument, error) {
	if in, ok := s.inst[ref]; ok {
		return in, nil
	}
	in, err := s.d.Resolver.Resolve(ref)
	if err != nil {
		return in, err
	}
	s.inst[ref] = in
	return in, nil
}

// execDay — день исполнения. Размещение — первый день торгов выпуском у обеих моделей:
// заявку подают до торгов, отчёт об исполнении автор пишет уже после.
func (s *sim) execDay(t Trade, in contract.Instrument, cal []time.Time) (time.Time, error) {
	if t.Placement {
		bars, err := s.bondBars(in.SecID)
		if err != nil {
			return time.Time{}, err
		}
		return firstOnOrAfter(cal, bars[0].Day), nil
	}
	post := dayOf(t.PostedAt)
	if s.p.Mode == Author {
		return firstOnOrAfter(cal, post), nil
	}
	return firstOnOrAfter(cal, post.AddDate(0, 0, 1)), nil
}

// liveCode — код, под которым бумага торгуется в этот день: после последнего бара
// старого кода и с первого бара преемника — преемник (TCSG → T с 2024-11-28).
func (s *sim) liveCode(secid string, day time.Time) string {
	for _, r := range s.d.CA.Renames {
		if r.From != secid {
			continue
		}
		old, err1 := s.shareBars(r.From)
		next, err2 := s.shareBars(r.To)
		if err1 != nil || err2 != nil || len(old) == 0 {
			return secid
		}
		last := old[len(old)-1].Day
		for _, b := range next {
			if b.Day.After(last) {
				if !day.Before(b.Day) {
					return s.liveCode(r.To, day)
				}
				break
			}
		}
	}
	return secid
}

func (s *sim) trade(t Trade, day time.Time) {
	in := s.inst[t.Ref]
	if in.Kind != "bond" {
		in.SecID = s.liveCode(in.SecID, day) // «Продал Тинькофф» в декабре 2024 — это уже T
	}
	qty, price, note := t.Qty, t.Price, ""
	if t.Placement {
		note = "размещение по номиналу"
	}
	if t.All {
		if qty = s.qty[in.SecID]; qty <= 0 {
			s.problem("пост %d: продать всё %s, а позиции нет", t.PostID, in.SecID)
			return
		}
		if s.p.Mode == Author && !t.Fixed {
			var ok bool
			if price, ok = s.allPrice(t, in, qty); !ok {
				return
			}
		}
	}
	if t.Fixed {
		note = "не рыночная: цена из правки"
	}
	if s.p.Mode == Follower && !t.Placement && !t.Fixed {
		var err error
		if price, note, err = s.marketUnit(in, day); err != nil {
			s.problem("пост %d %s: %v", t.PostID, in.SecID, err)
			return
		}
	}
	if t.Side == Sell && qty > s.qty[in.SecID]+1e-9 {
		have := s.qty[in.SecID]
		if have <= 0 {
			s.problem("пост %d: продажа %g %s, а позиции нет — пропущена покупка", t.PostID, qty, in.SecID)
			return
		}
		s.warn("пост %d: продажа %g %s, а в портфеле %g — продаём сколько есть (пропущена покупка?)", t.PostID, qty, in.SecID, have)
		qty = have
	}
	comm := 0.0
	if s.p.Mode == Follower && !t.Placement && !t.Fixed {
		comm = s.p.CommissionBps / 1e4
	}
	o := Op{Day: day, SecID: in.SecID, Qty: qty, Price: price, PostID: t.PostID, Note: note}
	if t.Side == Buy {
		o.Kind, o.Amount = "buy", -qty*price*(1+comm)
		s.qty[in.SecID] += qty
	} else {
		o.Kind, o.Amount = "sell", qty*price*(1-comm)
		s.qty[in.SecID] -= qty
	}
	s.op(o)
}

// resolve — количество и цена сделки по словам автора, сверенные с рынком дня поста.
// Делается один раз до обеих моделей: модели отличаются ценой исполнения, а не числом бумаг.
// «Продать всё» решается в момент сделки — там важна позиция.
func (s *sim) resolve(t *Trade, in contract.Instrument) bool {
	if in.Kind != "bond" {
		in.SecID = s.liveCode(in.SecID, dayOf(t.PostedAt)) // цены дня поста — под кодом, живым в тот день
	}
	if !t.Placement && in.Kind == "bond" && t.Side == Buy {
		// у выпуска не было торгов до поста — это заявка на размещение
		bars, err := s.bondBars(in.SecID)
		if err != nil || len(bars) == 0 {
			s.problem("пост %d %s: нет истории облигации: %v", t.PostID, in.SecID, err)
			return false
		}
		t.Placement = bars[0].Day.After(dayOf(t.PostedAt))
	}
	if t.All || t.Fixed {
		return true
	}
	if t.Amount <= 0 {
		unit, _, err := s.marketUnit(in, dayOf(t.PostedAt))
		if err != nil || unit <= 0 {
			s.problem("пост %d %s: суммы нет, а рыночной цены на день поста тоже", t.PostID, in.SecID)
			return false
		}
		t.Price = unit
		return true
	}
	lo, hi, err := s.dayRange(in, dayOf(t.PostedAt), t.Placement)
	if err != nil {
		s.problem("пост %d %s: %v", t.PostID, in.SecID, err)
		return false
	}
	if t.Placement && t.Qty == 0 {
		// «насыпал на 120000р»: штук = сумма / номинал (в рублях на день размещения)
		bars, _ := s.bondBars(in.SecID)
		face := bars[0].Face * s.rate(bars[0].FaceUnit, bars[0].Day)
		t.Qty = math.Round(t.Amount / face)
		t.Price = t.Amount / t.Qty
		return t.Qty > 0
	}
	// Автор ставит точку или запятую как разделитель тысяч: «13.267», «60,574р» — это 13 267 и 60 574.
	amounts := []float64{t.Amount}
	if t.Amount < 1000 && t.Amount != math.Trunc(t.Amount) {
		amounts = append(amounts, math.Round(t.Amount*1000*100)/100)
	}
	fits := func(q float64) bool {
		for _, a := range amounts {
			if u := a / q; q > 0 && u >= lo && u <= hi {
				t.Qty, t.Price = q, u
				return true
			}
		}
		return false
	}
	// Порядок важен: заявленные штуки; «1 лот (100шт) 32.67р» — вместо суммы цена за штуку
	// (иначе «1 лот» сойдётся как одна акция); лоты, если штуки разошлись с ними («86 лотов (125шт)»).
	if fits(t.Qty) {
		return true
	}
	if t.Qty > 1 && t.Amount >= lo && t.Amount <= hi {
		t.Price = t.Amount
		return true
	}
	if t.QtyAlt > 0 && fits(t.QtyAlt) {
		return true
	}
	// Количество не сходится ни в каком варианте, а сумма есть: количество из суммы и середины
	// дня, если так цена попадает в диапазон. У фондов денежного рынка это обычное дело
	// («LQDT 44315 шт на 514022р» при цене 1.55), у бумаг — опечатка в количестве.
	mid := (lo + hi) / 2
	for _, a := range amounts {
		if q := math.Round(a / mid); q > 0 && a/q >= lo && a/q <= hi {
			s.warn("пост %d %s: количество %g не сходится с суммой %.2f — берём %g по цене дня («%s»)", t.PostID, in.SecID, t.Qty, a, q, t.Line)
			t.Qty, t.Price = q, a/q
			return true
		}
	}
	s.problem("пост %d %s: %s %g шт на %.2f ₽ = %.4g за шт, а рынок дня %.4g…%.4g — сумма или количество не сходятся («%s»)",
		t.PostID, in.SecID, t.Side, t.Qty, t.Amount, t.Amount/t.Qty, lo, hi, t.Line)
	return false
}

// allPrice — цена продажи всей позиции: из суммы автора, если сходится с рынком, иначе рынок дня.
func (s *sim) allPrice(t Trade, in contract.Instrument, qty float64) (float64, bool) {
	if t.Amount > 0 {
		if lo, hi, err := s.dayRange(in, dayOf(t.PostedAt), false); err == nil {
			if u := t.Amount / qty; u >= lo && u <= hi {
				return u, true
			}
			s.warn("пост %d %s: продажа всей позиции (%g шт) на %.2f ₽ = %.4g за шт, рынок %.4g…%.4g — берём цену рынка", t.PostID, in.SecID, qty, t.Amount, t.Amount/qty, lo, hi)
		}
	}
	unit, _, err := s.marketUnit(in, dayOf(t.PostedAt))
	if err != nil {
		s.problem("пост %d %s: %v", t.PostID, in.SecID, err)
		return 0, false
	}
	return unit, true
}

// dayRange — допустимая цена за бумагу в день поста (с соседними днями: пост мог быть в выходной).
func (s *sim) dayRange(in contract.Instrument, day time.Time, placement bool) (float64, float64, error) {
	lo, hi := math.Inf(1), math.Inf(-1)
	if in.Kind == "bond" {
		bars, err := s.bondBars(in.SecID)
		if err != nil {
			return 0, 0, err
		}
		if placement {
			k := s.rate(bars[0].FaceUnit, bars[0].Day)
			f := bars[0].Face * k
			return f * (1 - bondTol), f*(1+bondTol) + bars[0].AccInt*k, nil
		}
		for _, b := range near(bars, day, func(b contract.BondBar) time.Time { return b.Day }) {
			if b.Low <= 0 || b.High <= 0 {
				continue
			}
			k := s.rate(b.FaceUnit, b.Day)
			lo = math.Min(lo, b.Low*b.Face/100*k)
			hi = math.Max(hi, (b.High*b.Face/100+b.AccInt)*k)
		}
		if math.IsInf(lo, 1) {
			return 0, 0, fmt.Errorf("нет сделок облигацией около %s", key(day))
		}
		return lo * (1 - bondTol), hi * (1 + bondTol), nil
	}
	bars, err := s.shareBars(in.SecID)
	if err != nil {
		return 0, 0, err
	}
	for _, b := range near(bars, day, func(b contract.Bar) time.Time { return b.Day }) {
		lo, hi = math.Min(lo, b.Low), math.Max(hi, b.High)
	}
	if math.IsInf(lo, 1) {
		return 0, 0, fmt.Errorf("нет торгов около %s", key(day))
	}
	return lo * (1 - shareTol), hi * (1 + shareTol), nil
}

// marketUnit — цена за бумагу на open дня исполнения (последователь).
func (s *sim) marketUnit(in contract.Instrument, day time.Time) (float64, string, error) {
	if in.Kind == "bond" {
		bars, err := s.bondBars(in.SecID)
		if err != nil {
			return 0, "", err
		}
		for _, b := range bars {
			if b.Day.Before(day) {
				continue
			}
			px := b.Open
			if px <= 0 {
				px = b.Close
			}
			if px > 0 {
				note := ""
				if !b.Day.Equal(day) {
					note = "первый день со сделками " + key(b.Day)
				}
				return (px*b.Face/100 + b.AccInt) * s.rate(b.FaceUnit, b.Day), note, nil
			}
			if b.Day.Sub(day) > 10*24*time.Hour {
				break
			}
		}
		return 0, "", fmt.Errorf("нет сделок облигацией с %s", key(day))
	}
	bars, err := s.shareBars(in.SecID)
	if err != nil {
		return 0, "", err
	}
	i := sort.Search(len(bars), func(i int) bool { return !bars[i].Day.Before(day) })
	if i == len(bars) || bars[i].Day.Sub(day) > 10*24*time.Hour {
		return 0, "", fmt.Errorf("нет торгов с %s", key(day))
	}
	return bars[i].Open, "", nil
}

// markPrice — оценка бумаги на конец дня: последняя цена, у облигации — плюс НКД дня.
func (s *sim) markPrice(secid string, day time.Time) float64 {
	if bars, ok := s.bond[secid]; ok {
		var px, face, acc float64
		unit := ""
		for _, b := range bars {
			if b.Day.After(day) {
				break
			}
			if b.Close > 0 {
				px = b.Close
			}
			face, acc, unit = b.Face, b.AccInt, b.FaceUnit
		}
		return (px*face/100 + acc) * s.rate(unit, day)
	}
	bars := s.share[secid]
	i := sort.Search(len(bars), func(i int) bool { return bars[i].Day.After(day) }) - 1
	if i < 0 {
		return 0
	}
	return bars[i].Close
}

// bondFlows — купоны (держали на конец дня перед реестром) и амортизации/погашение с выплатой в (prev, day].
func (s *sim) bondFlows(prev, day time.Time) {
	for _, secid := range s.held() {
		q := s.qty[secid]
		if q <= 0 {
			continue
		}
		in := contract.Instrument{}
		for _, x := range s.inst {
			if x.SecID == secid {
				in = x
			}
		}
		if in.Kind != "bond" {
			continue
		}
		flows, err := s.bondFlowsOf(secid)
		if err != nil {
			s.problem("%s: выплаты: %v", secid, err)
			continue
		}
		var maturity time.Time // погашение — последняя амортизация графика
		for _, f := range flows {
			if f.Amortizing && f.Day.After(maturity) {
				maturity = f.Day
			}
		}
		for _, f := range flows {
			if !f.Day.After(prev) || f.Day.After(day) || f.ValueRub <= 0 {
				continue
			}
			if f.Amortizing {
				s.op(Op{Day: day, Kind: "amortization", SecID: secid, Qty: q, Price: f.ValueRub, Amount: q * f.ValueRub})
				if f.Day.Equal(maturity) {
					s.qty[secid] = 0
				}
				continue
			}
			s.op(Op{Day: day, Kind: "coupon", SecID: secid, Qty: q, Price: f.ValueRub, Amount: q * f.ValueRub})
		}
	}
}

// dividends — выплаты с T-1 == day по акциям, которые держим на закрытии дня.
func (s *sim) dividends(day time.Time) {
	if s.d.Divs == nil {
		return
	}
	for _, secid := range s.held() {
		q := s.qty[secid]
		if q <= 0 {
			continue
		}
		if _, isShare := s.share[secid]; !isShare {
			continue
		}
		rows, err := s.dividendRows(secid)
		if err != nil {
			s.problem("%s: дивиденды: %v", secid, err)
			continue
		}
		for _, r := range rows {
			if r.lastBuy.Equal(day) {
				s.op(Op{Day: day, Kind: "dividend", SecID: secid, Qty: q, Price: r.perShare, Amount: q * r.perShare})
			}
		}
	}
}

// dividendRows — выплаты в фактическом масштабе (источник пересчитывает под поздние сплиты, см. ADR 0003).
func (s *sim) dividendRows(secid string) ([]divRow, error) {
	if r, ok := s.divs[secid]; ok {
		return r, nil
	}
	rows, err := s.d.Divs.Dividends(secid)
	if err != nil {
		return nil, err
	}
	var out []divRow
	for _, r := range rows {
		day, err := time.Parse(time.DateOnly, r.LastBuyDay)
		if err != nil {
			continue
		}
		k := 1.0
		for d, f := range s.split[secid] {
			if d > r.LastBuyDay {
				k *= f
			}
		}
		per := r.Amount * k
		for _, fx := range s.d.CA.Dividends {
			if fx.SecID == secid && fx.LastBuyDay == r.LastBuyDay && (fx.Source == 0 || fx.Source == r.Amount) {
				per = fx.Amount
			}
		}
		out = append(out, divRow{day, per})
	}
	s.divs[secid] = out
	return out, nil
}

func (s *sim) shareBars(secid string) ([]contract.Bar, error) {
	if b, ok := s.share[secid]; ok {
		return b, nil
	}
	b, err := s.d.Shares.DailyBars(secid)
	if err != nil {
		return nil, err
	}
	s.share[secid] = b
	return b, nil
}

func (s *sim) bondBars(secid string) ([]contract.BondBar, error) {
	if b, ok := s.bond[secid]; ok {
		return b, nil
	}
	b, err := s.d.Bonds.BondBars(secid)
	if err != nil {
		return nil, err
	}
	for _, x := range b {
		if rub(x.FaceUnit) || s.fx[x.FaceUnit] != nil {
			continue
		}
		if s.d.FX == nil {
			return nil, fmt.Errorf("номинал в %s, а курсов нет", x.FaceUnit)
		}
		r, err := s.d.FX.Rates(x.FaceUnit)
		if err != nil {
			return nil, err
		}
		s.fx[x.FaceUnit] = r
	}
	s.bond[secid] = b
	return b, nil
}

func rub(unit string) bool { return unit == "" || unit == "RUB" || unit == "SUR" }

// rate — рублей за единицу валюты номинала: курс ЦБ на день или ближайший предыдущий.
func (s *sim) rate(unit string, day time.Time) float64 {
	if rub(unit) {
		return 1
	}
	rs := s.fx[unit]
	i := sort.Search(len(rs), func(i int) bool { return rs[i].Day.After(day) }) - 1
	if i < 0 {
		return math.NaN()
	}
	return rs[i].Value
}

func (s *sim) bondFlowsOf(secid string) ([]contract.BondFlow, error) {
	if f, ok := s.flows[secid]; ok {
		return f, nil
	}
	f, err := s.d.Bonds.BondFlows(secid)
	if err != nil {
		return nil, err
	}
	s.flows[secid] = f
	return f, nil
}

func tradingDays(bench []contract.Bar, from time.Time) []time.Time {
	var out []time.Time
	for _, b := range bench {
		if !b.Day.Before(from) {
			out = append(out, b.Day)
		}
	}
	return out
}

func firstOnOrAfter(cal []time.Time, d time.Time) time.Time {
	i := sort.Search(len(cal), func(i int) bool { return !cal[i].Before(d) })
	if i == len(cal) {
		return d // в будущем — исполнится, когда наступит торговый день
	}
	return cal[i]
}

// near — бары дня поста; нет торгов в этот день — ближайшие до и после (пост в выходной).
func near[B any](bars []B, day time.Time, at func(B) time.Time) []B {
	i := sort.Search(len(bars), func(i int) bool { return !at(bars[i]).Before(day) })
	if i < len(bars) && at(bars[i]).Equal(day) {
		return bars[i : i+1]
	}
	var out []B
	if i > 0 && day.Sub(at(bars[i-1])) <= 5*24*time.Hour {
		out = append(out, bars[i-1])
	}
	if i < len(bars) && at(bars[i]).Sub(day) <= 5*24*time.Hour {
		out = append(out, bars[i])
	}
	return out
}

func key(t time.Time) string { return t.Format(time.DateOnly) }
