package paperstrat

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/paper"
)

// OFZAuctions — D из 0018: неконкурентная заявка на каждый аукцион ОФЗ по средневзвешенной,
// выход по открытию TQOB следующего торгового дня (нет открытия — по официальному закрытию).
type OFZAuctions struct {
	StartDay      time.Time
	Cap, PerTrade float64 // 1 000 000; 500 000 на аукцион
	Auctions      func(time.Time) ([]contract.Auction, error)
	Bars          func(string, time.Time) ([]contract.BondBar, error) // TQOB, Close — официальное закрытие
	Flows         func(string) ([]contract.BondFlow, error)
	MM            func() ([]contract.Bar, error) // LQDT: торговый календарь
	mu            sync.Mutex                     // marks — для Mark (админка, пока идёт пересчёт)
	marks         map[string][]contract.BondBar
	marksAt       time.Time
}

func (o *OFZAuctions) Name() string { return "ofz-auctions" }
func (o *OFZAuctions) Title() string {
	return "Аукционы ОФЗ → продажа утром (идея 11)"
}
func (o *OFZAuctions) Benchmark() string { return "LQDT" }
func (o *OFZAuctions) Capital() float64  { return o.Cap }
func (o *OFZAuctions) Start() time.Time  { return o.StartDay }

// auctionID — номер события в журнале: один выпуск размещают много раз, сделки не должны слипаться.
func auctionID(day time.Time) int64 {
	y, m, d := day.Date()
	return int64(y*10000 + int(m)*100 + d)
}

func (o *OFZAuctions) Ops(asOf time.Time, _ paper.Memory) ([]contract.Op, error) {
	mm, err := o.MM()
	if err != nil {
		return nil, err
	}
	var cal []time.Time
	for _, b := range mm {
		if !b.Day.Before(o.StartDay) && b.Day.Before(asOf) {
			cal = append(cal, b.Day)
		}
	}
	type action struct {
		day      time.Time
		order    int // продажа, купон, покупка — деньги от утренней продажи доступны аукциону
		kind     string
		secid    string
		id       int64
		px, unit float64 // px — % номинала (для журнала), unit — руб. за бумагу
		reason   string
	}
	var acts []action
	bars := map[string][]contract.BondBar{}
	for _, day := range cal {
		as, err := o.Auctions(day)
		if err != nil {
			return nil, err
		}
		for _, a := range as {
			id := auctionID(day)
			acts = append(acts, action{day: day, order: 2, kind: "buy", secid: a.SecID, id: id, px: a.AvgPrice,
				unit: a.AvgPrice/100*a.Face + a.AccInt, reason: fmt.Sprintf("аукцион, средневзвешенная %.4f%%", a.AvgPrice)})
			b, ok := bars[a.SecID]
			if !ok {
				if b, err = o.Bars(a.SecID, o.StartDay); err != nil {
					return nil, err
				}
				bars[a.SecID] = b
			}
			i := sort.Search(len(b), func(i int) bool { return b[i].Day.After(day) })
			if i == len(b) || !b[i].Day.Before(asOf) {
				continue // следующего торгового дня ещё не было — позиция открыта
			}
			x := b[i]
			px, how := x.Open, "открытие"
			if px <= 0 {
				px, how = x.Close, "официальное закрытие"
			}
			if px <= 0 {
				return nil, fmt.Errorf("%s %s: нет ни открытия, ни закрытия", a.SecID, x.Day.Format(time.DateOnly))
			}
			flows, err := o.Flows(a.SecID)
			if err != nil {
				return nil, err
			}
			for _, f := range flows {
				if f.Day.After(day) && !f.Day.After(x.Day) && f.ValueRub > 0 {
					kind := "coupon"
					if f.Amortizing {
						kind = "amortization"
					}
					acts = append(acts, action{day: f.Day, order: 1, kind: kind, secid: a.SecID, id: id, unit: f.ValueRub})
				}
			}
			acts = append(acts, action{day: x.Day, order: 0, kind: "sell", secid: a.SecID, id: id, px: px,
				unit: px/100*x.Face + x.AccInt, reason: "TQOB, " + how + " следующего дня"})
		}
	}
	sort.SliceStable(acts, func(i, j int) bool {
		if !acts[i].day.Equal(acts[j].day) {
			return acts[i].day.Before(acts[j].day)
		}
		if acts[i].order != acts[j].order {
			return acts[i].order < acts[j].order
		}
		return acts[i].secid < acts[j].secid
	})

	type key struct {
		id    int64
		secid string
	}
	cash := o.Cap
	held := map[key]float64{}
	var ops []contract.Op
	for _, a := range acts {
		k := key{a.id, a.secid}
		op := contract.Op{Day: a.day.Format(time.DateOnly), SecID: a.secid, PostID: a.id, Reason: a.reason}
		switch a.kind {
		case "buy":
			unit := a.unit * (1 + cost)
			q := math.Floor(math.Min(o.PerTrade, cash) / unit)
			if q < 1 {
				op.Kind, op.Reason = "skip", "нет кэша"
				ops = append(ops, op)
				continue
			}
			cash -= q * unit
			held[k] = q
			op.Kind, op.Qty, op.Price, op.Amount = "buy", q, a.px, round2(-q*unit)
		case "coupon", "amortization":
			q := held[k]
			if q == 0 {
				continue
			}
			cash += q * a.unit
			op.Kind, op.Qty, op.Price, op.Amount = a.kind, q, a.unit, round2(q*a.unit)
		case "sell":
			q := held[k]
			if q == 0 {
				continue
			}
			amt := q * a.unit * (1 - cost)
			cash += amt
			delete(held, k)
			op.Kind, op.Qty, op.Price, op.Amount = "sell", q, a.px, round2(amt)
		}
		ops = append(ops, op)
	}
	return ops, nil
}

func (o *OFZAuctions) Mark(secid string, day time.Time) (float64, bool) {
	o.mu.Lock()
	if o.marks == nil || time.Since(o.marksAt) > time.Hour {
		o.marks, o.marksAt = map[string][]contract.BondBar{}, time.Now()
	}
	b, ok := o.marks[secid]
	if !ok {
		var err error
		if b, err = o.Bars(secid, o.StartDay); err != nil {
			o.mu.Unlock()
			return 0, false
		}
		o.marks[secid] = b
	}
	o.mu.Unlock()
	i := sort.Search(len(b), func(i int) bool { return b[i].Day.After(day) }) - 1
	if i < 0 || b[i].Close <= 0 {
		return 0, false
	}
	return b[i].Close/100*b[i].Face + b[i].AccInt, true
}
