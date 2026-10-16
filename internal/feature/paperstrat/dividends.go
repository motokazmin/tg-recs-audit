// Package paperstrat — стратегии для paper-проверки вперёд (регистрация 0013).
// Каждая — функция «операции с даты старта по данным к asOf», как в бэктестах 0007/0011.
package paperstrat

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/feature/divevents"
	"tg-recs-audit/internal/paper"
)

const cost = 0.0005 // на ногу

// Dividends — A из 0013: рекомендация дивидендов → покупка → выход в день отсечки.
type Dividends struct {
	StartDay time.Time
	Cap      float64 // 1 000 000
	PerTrade float64 // 50 000
	Source   contract.ChannelSource
	Known    map[string]bool
	Prices   contract.PriceHistory
	Divs     contract.DividendSource
	cache    map[string][]contract.Bar // только для Ops
	marks    marks                     // для Mark: читает админка, пока идёт пересчёт
}

func (d *Dividends) Name() string { return "dividends" }
func (d *Dividends) Title() string {
	return "Рекомендации дивидендов → до отсечки (идея 10)"
}
func (d *Dividends) Benchmark() string { return "MCFTR" }
func (d *Dividends) Capital() float64  { return d.Cap }
func (d *Dividends) Start() time.Time  { return d.StartDay }

func (d *Dividends) Ops(asOf time.Time, mem paper.Memory) ([]contract.Op, error) {
	d.cache = map[string][]contract.Bar{}
	_, msgs, err := d.Source.Messages()
	if err != nil {
		return nil, err
	}
	// Событие запоминается при первом появлении: удалённый или исправленный пост не меняет прошлое.
	evs, _ := divevents.Parse(msgs, d.Known)
	for _, e := range evs {
		if postDay(e.PostedAt).Before(d.StartDay) {
			continue
		}
		k := "event:" + strconv.FormatInt(e.PostID, 10)
		if _, ok := mem.Get(k); !ok {
			b, _ := json.Marshal(e)
			mem.Set(k, string(b))
		}
	}
	var events []divevents.Event
	for _, k := range mem.Keys("event:") {
		v, _ := mem.Get(k)
		var e divevents.Event
		if err := json.Unmarshal([]byte(v), &e); err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		events = append(events, e)
	}
	sort.Slice(events, func(i, j int) bool {
		if !events[i].PostedAt.Equal(events[j].PostedAt) {
			return events[i].PostedAt.Before(events[j].PostedAt)
		}
		return events[i].PostID < events[j].PostID
	})

	type action struct {
		day   time.Time
		order int // в один день: продажа, дивиденд, покупка — деньги от продажи доступны покупке
		e     divevents.Event
		px    float64
		kind  string
	}
	var acts []action
	for _, e := range events {
		bars, err := d.bars(e.SecID, asOf)
		if err != nil {
			return nil, err
		}
		i := sort.Search(len(bars), func(i int) bool { return bars[i].Day.After(postDay(e.PostedAt)) })
		if i == len(bars) {
			continue // торгового дня после поста ещё не было
		}
		if bars[i].Day.After(e.LastBuy) {
			acts = append(acts, action{day: bars[i].Day, order: 2, e: e, kind: "late"})
			continue
		}
		acts = append(acts, action{day: bars[i].Day, order: 2, e: e, px: bars[i].Open, kind: "buy"})
		x := sort.Search(len(bars), func(k int) bool { return bars[k].Day.After(e.LastBuy) })
		if x == len(bars) {
			continue // отсечка ещё не наступила
		}
		ex := bars[x]
		acts = append(acts, action{day: ex.Day, order: 0, e: e, px: ex.Close, kind: "sell"})
		if day, ok, err := d.confirmed(e, ex.Day, asOf, mem); err != nil {
			return nil, err
		} else if ok {
			acts = append(acts, action{day: day, order: 1, e: e, kind: "dividend"})
		}
	}
	sort.SliceStable(acts, func(i, j int) bool {
		if !acts[i].day.Equal(acts[j].day) {
			return acts[i].day.Before(acts[j].day)
		}
		if acts[i].order != acts[j].order {
			return acts[i].order < acts[j].order
		}
		return acts[i].e.PostID < acts[j].e.PostID
	})

	cash := d.Cap
	held := map[int64]float64{}
	var ops []contract.Op
	for _, a := range acts {
		base := contract.Op{Day: a.day.Format(time.DateOnly), SecID: a.e.SecID, PostID: a.e.PostID, PostedAt: a.e.PostedAt}
		switch a.kind {
		case "late":
			base.Kind, base.Reason = "skip", "первый день после поста позже T-1"
			ops = append(ops, base)
		case "buy":
			unit := a.px * (1 + cost)
			q := math.Floor(d.PerTrade / unit)
			if q < 1 || q*unit > cash {
				base.Kind, base.Reason = "skip", "нет кэша или акция дороже суммы на сделку"
				ops = append(ops, base)
				continue
			}
			cash -= q * unit
			held[a.e.PostID] = q
			base.Kind, base.Qty, base.Price, base.Amount = "buy", q, a.px, round2(-q*unit)
			base.Reason = fmt.Sprintf("T-1 %s, дивиденд %.4g", a.e.LastBuy.Format(time.DateOnly), a.e.Div)
			ops = append(ops, base)
		case "sell":
			q := held[a.e.PostID]
			if q == 0 {
				continue
			}
			amt := q * a.px * (1 - cost)
			cash += amt
			base.Kind, base.Qty, base.Price, base.Amount, base.Reason = "sell", q, a.px, round2(amt), "день отсечки"
			ops = append(ops, base)
		case "dividend":
			q := held[a.e.PostID]
			if q == 0 {
				continue
			}
			amt := q * a.e.Div
			cash += amt
			base.Kind, base.Qty, base.Price, base.Amount, base.Reason = "dividend", q, a.e.Div, round2(amt), "выплата подтверждена smart-lab"
			ops = append(ops, base)
		}
	}
	return ops, nil
}

// confirmed — выплата подтверждена источником; день зачисления — первый запуск, увидевший её,
// но не раньше отсечки (запоминается: иначе позднее подтверждение переписало бы прошлое).
func (d *Dividends) confirmed(e divevents.Event, exDay, asOf time.Time, mem paper.Memory) (time.Time, bool, error) {
	k := "divpaid:" + strconv.FormatInt(e.PostID, 10)
	if v, ok := mem.Get(k); ok {
		t, err := time.Parse(time.DateOnly, v)
		return t, err == nil, err
	}
	// Сначала источник, потом срок: иначе выплата, подтверждённая позже 60 дней (или старое
	// событие при первом запуске), пропадала бы — цена упала на дивиденд, а он не начислен.
	rows, err := d.Divs.Dividends(e.SecID)
	if err != nil {
		return time.Time{}, false, err
	}
	for _, r := range rows {
		t, err := time.Parse(time.DateOnly, r.LastBuyDay)
		if err != nil {
			continue
		}
		if diff := t.Sub(e.LastBuy).Hours() / 24; diff >= -7 && diff <= 7 {
			day := asOf.AddDate(0, 0, -1) // последний закрытый день
			if day.Before(exDay) {
				day = exDay
			}
			mem.Set(k, day.Format(time.DateOnly))
			return day, true, nil
		}
	}
	return time.Time{}, false, nil // нет — ещё ждём; не подтверждена за 60 дней после отсечки — считаем отменённой (в разборе)
}

func (d *Dividends) bars(secid string, asOf time.Time) ([]contract.Bar, error) {
	if b, ok := d.cache[secid]; ok {
		return b, nil
	}
	b, err := d.Prices.DailyBars(secid)
	if err != nil {
		return nil, err
	}
	b = b[:sort.Search(len(b), func(i int) bool { return !b[i].Day.Before(asOf) })]
	d.cache[secid] = b
	return b, nil
}

func (d *Dividends) Mark(secid string, day time.Time) (float64, bool) {
	b, ok := d.marks.get(secid, d.Prices.DailyBars)
	if !ok {
		return 0, false
	}
	i := sort.Search(len(b), func(i int) bool { return b[i].Day.After(day) }) - 1
	if i < 0 {
		return 0, false
	}
	return b[i].Close, true
}

// marks — кэш баров для оценки позиций, со своим замком.
type marks struct {
	mu sync.Mutex
	m  map[string][]contract.Bar
	at time.Time
}

func (k *marks) get(secid string, load func(string) ([]contract.Bar, error)) ([]contract.Bar, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil || time.Since(k.at) > time.Hour {
		k.m, k.at = map[string][]contract.Bar{}, time.Now()
	}
	if b, ok := k.m[secid]; ok {
		return b, true
	}
	b, err := load(secid)
	if err != nil {
		return nil, false
	}
	k.m[secid] = b
	return b, true
}

func postDay(t time.Time) time.Time {
	t = t.In(paper.MSK)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
