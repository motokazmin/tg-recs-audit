// Package mentions — какие тикеры упоминаются в постах и когда.
//
// Это ещё не сигналы, только кандидаты: понять, о каких бумагах канал пишет
// и какая история цен понадобится. Токен засчитывается, если совпал с кодом
// бумаги биржи (включая делистнутые).
package mentions

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"tg-recs-audit/internal/contract"
)

var (
	word   = regexp.MustCompile(`[A-Za-z0-9]+`)
	ticker = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,5}$`)
)

// Совпадают с кодами бумаг, но в постах почти всегда значат другое.
var stopwords = map[string]bool{
	"IPO": true, "ETF": true, "USD": true, "EUR": true, "CNY": true, "RUB": true,
	"CEO": true, "GDP": true, "AI": true, "IT": true, "TV": true, "OK": true, "PE": true,
}

var tradable = map[string]bool{
	"common_share": true, "preferred_share": true, "depositary_receipt": true,
	"public_ppif": true, "interval_ppif": true, "private_ppif": true,
	"exchange_ppif": true, "etf_ppif": true,
}

type Mention struct {
	SecID       string
	Count       int // постов с упоминанием
	Strong      int // из них $TICKER, #TICKER или тикер в ссылке — почти наверняка бумага
	First, Last time.Time
}

// Index — тикеры в каждом посте; строится один раз на канал.
type Index struct {
	known map[string]bool
}

func NewIndex(universe []contract.Security) Index {
	known := map[string]bool{}
	for _, s := range universe {
		if tradable[s.Type] && !stopwords[s.SecID] {
			known[s.SecID] = true
		}
	}
	return Index{known: known}
}

// InPost — тикеры поста: значение true, если упоминание «сильное».
func (x Index) InPost(m contract.Message) map[string]bool {
	out := map[string]bool{}
	for _, t := range m.Tags {
		if t = strings.ToUpper(t); x.known[t] {
			out[t] = true
		}
	}
	// Тикер в пути ссылки: investmint.ru/sibn/, smart-lab.ru/q/SBER/
	for _, l := range m.Links {
		u, err := url.Parse(l)
		if err != nil {
			continue
		}
		for _, seg := range strings.Split(u.Path, "/") {
			if t := strings.ToUpper(seg); ticker.MatchString(t) && x.known[t] {
				out[t] = true
			}
		}
	}
	for _, w := range word.FindAllString(m.Text, -1) {
		if ticker.MatchString(w) && x.known[w] {
			if _, ok := out[w]; !ok {
				out[w] = false
			}
		}
	}
	return out
}

func Find(msgs []contract.Message, x Index) []Mention {
	acc := map[string]*Mention{}
	for _, m := range msgs {
		d := m.PostedAt
		for t, strong := range x.InPost(m) {
			a := acc[t]
			if a == nil {
				a = &Mention{SecID: t, First: d, Last: d}
				acc[t] = a
			}
			a.Count++
			if strong {
				a.Strong++
			}
			if d.Before(a.First) {
				a.First = d
			}
			if d.After(a.Last) {
				a.Last = d
			}
		}
	}
	out := make([]Mention, 0, len(acc))
	for _, a := range acc {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].SecID < out[j].SecID
	})
	return out
}
