package portfolio

import (
	"fmt"
	"sort"
	"time"
)

// credit — выплата в фактическом масштабе своего времени.
type credit struct {
	lastBuy   time.Time
	perShare  float64
	confirmed bool // сумма из ручной правки — сверку доходности не проходит
}

// Границы правдоподобной доходности выплаты к close на T-1. Ловят промах масштаба
// в 10–100 раз (пропущенный сплит), но не в 2–5: так ошибся smart-lab по VTBR 2020–2021,
// это видно только сверкой источников — поправки в corporate_actions.json → dividends.
const (
	minYield = 0.002 // ниже — почти наверняка промах ×100; законную мелкую выплату подтвердить поправкой
	maxYield = 0.35
)

// dividends — выплаты кода. Источник пересчитывает старые выплаты под последующие сплиты
// (GMKN 2023: 9.153 вместо 915.33), поэтому сумма умножается на сплиты этого кода после T-1.
// У старого кода без своих строк (TCSG — страницы нет, история на /q/T/) берутся строки
// преемника, переведённые в старые акции через ratio.
func (e *engine) dividends(code string) ([]credit, error) {
	if e.divSrc == nil {
		return nil, nil
	}
	if cs, ok := e.divs[code]; ok {
		return cs, nil
	}
	rows, err := e.divSrc.Dividends(code)
	if err != nil {
		return nil, err
	}
	scaleCode, ratio := code, 1.0
	if len(rows) == 0 {
		if r, ok := e.renames[code]; ok {
			if rows, err = e.divSrc.Dividends(r.To); err != nil {
				return nil, err
			}
			scaleCode, ratio = r.To, r.Ratio
		}
	}
	var cs []credit
	for _, r := range rows {
		day, err := time.Parse(time.DateOnly, r.LastBuyDay)
		if err != nil {
			return nil, fmt.Errorf("дивиденд %s: дата %q: %w", code, r.LastBuyDay, err)
		}
		k := e.splitsAfter(scaleCode, r.LastBuyDay) * ratio
		c := credit{lastBuy: day, perShare: r.Amount * k}
		for _, f := range e.divFixes {
			if f.SecID == code && f.LastBuyDay == r.LastBuyDay && (f.Source == 0 || f.Source == r.Amount) {
				c.perShare, c.confirmed = f.Amount, true
			}
		}
		cs = append(cs, c)
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].lastBuy.Before(cs[j].lastBuy) })
	e.divs[code] = cs
	return cs, nil
}

func (e *engine) splitsAfter(code, day string) float64 {
	k := 1.0
	for d, f := range e.splits[code] {
		if d > day {
			k *= f
		}
	}
	return k
}

// checkScale — доходность выплаты к close ISS около T-1 должна быть правдоподобной.
func (e *engine) checkScale(code string, c credit) (string, error) {
	if c.confirmed {
		return "", nil
	}
	bars, err := e.bars(code)
	if err != nil {
		return "", err
	}
	i := sort.Search(len(bars), func(i int) bool { return bars[i].Day.After(c.lastBuy) }) - 1
	if i < 0 || c.lastBuy.Sub(bars[i].Day) > 10*24*time.Hour {
		return "", nil // сверить не с чем
	}
	if y := c.perShare / bars[i].Close; y < minYield || y > maxYield {
		return fmt.Sprintf("%s: дивиденд с T-1 %s — %.4g ₽ при close %.4g, доходность %.3g%%: масштаб не сходится, сплит в таблице или поправка в dividends?",
			code, key(c.lastBuy), c.perShare, bars[i].Close, 100*y), nil
	}
	return "", nil
}
