// Package placements — купить облигацию по номиналу на размещении и продать через N дней:
// есть ли у механизма преимущество перед денежным рынком (LQDT). Регистрация — 0006.
package placements

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"tg-recs-audit/internal/contract"
)

// Market — что нужно проверке от рынка (порт на стороне потребителя).
type Market interface {
	Card(secid string) (map[string]string, error)
	BondHead(secid string, till time.Time) ([]contract.BondBar, error)
	BondFlows(secid string) ([]contract.BondFlow, error)
}

type Bond struct {
	SecID     string
	Name      string
	Issue     time.Time
	Coupon    float64 // % годовых из карточки; NaN — не задан (флоатер)
	Qual      bool
	IssueSize float64 // руб.
}

// noteName — имена структурных нот, у которых в карточке есть COUPONFREQUENCY.
var noteName = regexp.MustCompile(`(?i)структур|ПБО-\d+Р-К|ВТБ С-|ВТБ \(ПАО\) С-|RIB|CIB`)

// Universe — отбор по регистрации 0006 из списка облигаций и карточек.
func Universe(all []contract.Security, card func(string) (map[string]string, error), from, to time.Time) ([]Bond, map[string]int, error) {
	skip := map[string]int{}
	var out []Bond
	for _, s := range all {
		if s.Type != "corporate_bond" && s.Type != "exchange_bond" {
			continue
		}
		if !strings.HasPrefix(s.SecID, "RU000A") || s.SecID < "RU000A105" {
			continue
		}
		c, err := card(s.SecID)
		if err != nil {
			return nil, nil, err
		}
		issue, err := time.Parse(time.DateOnly, c["ISSUEDATE"])
		switch {
		case err != nil || issue.Before(from) || issue.After(to):
			skip["дата размещения вне окна"]++
			continue
		case c["FACEUNIT"] != "SUR" && c["FACEUNIT"] != "RUB":
			skip["номинал не в рублях"]++
			continue
		case strings.Contains(c["NAME"], "ИОС"):
			skip["структурная нота (ИОС)"]++
			continue
		}
		coupon := math.NaN()
		if v, err := strconv.ParseFloat(c["COUPONPERCENT"], 64); err == nil {
			coupon = v
		}
		// Структурные ноты: купон 0.01% (СберИОС) — или купона в карточке нет вовсе (Сбер CIB-SN,
		// ВЭБ.РФ ПБО-К, банковские ноты). NaN < 1 — ложь, поэтому без второй проверки ноты проходили (0007).
		if coupon < 1 {
			skip["купон < 1% (структурная)"]++
			continue
		}
		// Купона нет и у обычных выпусков: у флоатеров и у погашенных (ISS убирает купон из карточки).
		// Их отличает COUPONFREQUENCY и имя не ноты; без этого выборка теряла 41% выпусков (0034, 0035).
		if math.IsNaN(coupon) && c["COUPONVALUE"] == "" && (c["COUPONFREQUENCY"] == "" || noteName.MatchString(c["NAME"])) {
			skip["купона в карточке нет (нота, дисконтная)"]++
			continue
		}
		face, _ := strconv.ParseFloat(c["INITIALFACEVALUE"], 64)
		size, _ := strconv.ParseFloat(c["ISSUESIZE"], 64)
		out = append(out, Bond{SecID: s.SecID, Name: c["NAME"], Issue: issue, Coupon: coupon,
			Qual: c["ISQUALIFIEDINVESTORS"] == "1", IssueSize: size * face})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Issue.Before(out[j].Issue) })
	return out, skip, nil
}

// Obs — одно размещение на одном горизонте.
type Obs struct {
	Bond
	N        int
	Ret      float64 // доход от номинала: цена + НКД + выплаты − комиссия
	MM       float64 // LQDT за то же окно
	Phase    string  // растёт | стоит | падает — по 20-дневной доходности LQDT
	Spread   float64 // купон − доходность LQDT на размещении, п.п.
	NoTrades bool
	NotPar   float64 // ≠ 0 — цена первого дня торгов, %: размещение не по номиналу, вход неизвестен
}

// Размещение по номиналу: цена первого дня торгов в этих границах. Вне них — дисконтный или
// особый выпуск (ВЭБ.РФ ПБО-К, купон 1–2%), допущение «вход по 100%» неверно (0007).
const parLo, parHi = 97.0, 103.0

func (o Obs) Excess() float64 { return o.Ret - o.MM }

const exitCommission = 0.0005

// Evaluate считает доход размещения на горизонтах ns по календарю LQDT.
func Evaluate(b Bond, m Market, lqdt []contract.Bar, ns []int) ([]Obs, error) {
	cal := lqdt
	i0 := sort.Search(len(cal), func(i int) bool { return !cal[i].Day.Before(b.Issue) })
	if i0 == len(cal) {
		return nil, nil
	}
	maxN := ns[len(ns)-1]
	if i0+maxN >= len(cal) {
		return nil, nil // окно ещё не закончилось
	}
	bars, err := m.BondHead(b.SecID, cal[i0+maxN].Day)
	if err != nil {
		return nil, err
	}
	flows, err := m.BondFlows(b.SecID)
	if err != nil {
		return nil, err
	}
	face := 1000.0
	if len(bars) > 0 && bars[0].Face > 0 {
		face = bars[0].Face
	}
	notPar := 0.0
	if len(bars) > 0 {
		px := bars[0].Open
		if px <= 0 {
			px = bars[0].Close
		}
		if px > 0 && (px < parLo || px > parHi) {
			notPar = px
		}
	}
	y0 := Yield20(cal, i0)
	var out []Obs
	for _, n := range ns {
		exit := cal[i0+n].Day
		o := Obs{Bond: b, N: n, MM: cal[i0+n].Close/cal[i0].Close - 1, Spread: b.Coupon - 100*y0, NotPar: notPar}
		switch dy := Yield20(cal, i0+n) - y0; {
		case dy > 0.01:
			o.Phase = "растёт"
		case dy < -0.01:
			o.Phase = "падает"
		default:
			o.Phase = "стоит"
		}
		var last *contract.BondBar
		for k := range bars {
			if bars[k].Day.After(exit) {
				break
			}
			if bars[k].Close > 0 && bars[k].Day.After(b.Issue) {
				last = &bars[k]
			}
		}
		if last == nil {
			o.NoTrades = true
			out = append(out, o)
			continue
		}
		// Возврат номинала — по его уменьшению в барах, а не по графику: ипотечные облигации
		// ДОМ.РФ гасятся внепланово, в графике ISS этого нет (0007). Из выплат — только купоны.
		value := last.Close*last.Face/100 + last.AccInt + (face - last.Face)
		for _, f := range flows {
			if !f.Amortizing && f.Day.After(b.Issue) && !f.Day.After(exit) {
				value += f.ValueRub
			}
		}
		o.Ret = value*(1-exitCommission)/face - 1
		out = append(out, o)
	}
	return out, nil
}

// Yield20 — годовая доходность LQDT за 20 торговых дней до i: прокси ключевой ставки.
func Yield20(cal []contract.Bar, i int) float64 {
	j := max(0, i-20)
	days := cal[i].Day.Sub(cal[j].Day).Hours() / 24
	if days <= 0 {
		return 0
	}
	return math.Pow(cal[i].Close/cal[j].Close, 365/days) - 1
}

// Stat — сводка превышения над LQDT.
type Stat struct {
	N, Weeks        int
	Mean, Median    float64
	TWeeks          float64 // t по неделям размещения
	Positive        float64 // доля с превышением > 0
	MeanRet, MeanMM float64
}

func Summarize(obs []Obs) Stat {
	var s Stat
	var ex []float64
	week := map[string][]float64{}
	for _, o := range obs {
		if o.NoTrades || o.NotPar != 0 {
			continue
		}
		e := o.Excess()
		ex = append(ex, e)
		s.MeanRet += o.Ret
		s.MeanMM += o.MM
		y, w := o.Issue.ISOWeek()
		k := strconv.Itoa(y) + "-" + strconv.Itoa(w)
		week[k] = append(week[k], e)
	}
	s.N = len(ex)
	if s.N == 0 {
		return s
	}
	s.MeanRet /= float64(s.N)
	s.MeanMM /= float64(s.N)
	var pos int
	for _, e := range ex {
		s.Mean += e
		if e > 0 {
			pos++
		}
	}
	s.Mean /= float64(s.N)
	s.Positive = float64(pos) / float64(s.N)
	sort.Float64s(ex)
	s.Median = ex[len(ex)/2]
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
