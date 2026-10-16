// Package signals — торговые сигналы по фьючерсам FORTS карточкой (Exper_Trading):
// инструмент и контракт, тип ордера, цена, стоп, тейк, срок. Карточка исполняется правилом
// (регистрация 0008), ручное ведение постами не повторяется.
package signals

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"tg-recs-audit/internal/contract"
)

type Kind string

const (
	Limit  Kind = "limit"
	Stop   Kind = "stop"
	Market Kind = "market"
)

type Signal struct {
	PostID   int64
	PostedAt time.Time // московское время в зоне UTC — как свечи ISS
	Asset    string    // как в карточке: BR, NG, SILV, Si…
	SecID    string    // код контракта: BRX6
	Long     bool
	Kind     Kind
	Price    float64 // 0 у входа по рынку
	Stop     float64
	Take     float64   // 0 — «Открытый»
	Until    time.Time // ордер живёт до (не включая); нулевое — «До отмены», решает движок
}

// Unparsed — карточка, которую не удалось превратить в сигнал.
type Unparsed struct {
	PostID int64
	Reason string
}

// Префиксы кодов FORTS по названию инструмента в карточке.
var prefix = map[string]string{
	"BR": "BR", "BRENT": "BR", "NG": "NG", "SILV": "SV", "GOLD": "GD", "PLT": "PT", "SI": "Si", "EU": "Eu",
	"ED": "ED", "CNY": "CR", "SBRF": "SR", "GAZR": "GZ", "RTS": "RI", "MIX": "MX", "MXI": "MM",
	"VTBR": "VB", "LKOH": "LK", "MGNT": "MN", "ROSN": "RN", "SPYF": "SF", "SBER": "SR",
}

const months = "FGHJKMNQUVXZ"

var (
	cardRe  = regexp.MustCompile(`#\s*([A-Za-z]{2,5})\s*-\s*(\d{1,2})\.(\d{2})`)
	num     = `(\d[\d\s\x{00a0}]*(?:[.,]\d+)?)`
	typeRe  = regexp.MustCompile(`(?i)Тип сделки:\s*([^\n▪▫]*?)(?:\s*[▪▫]|\s*Цена|\n|$)`)
	priceRe = regexp.MustCompile(`(?i)Цена:\s*` + num)
	stopRe  = regexp.MustCompile(`(?i)Стоп\s*лосс:\s*` + num)
	takeRe  = regexp.MustCompile(`(?i)Тейк\s*профит:\s*(Открытый|` + num[1:len(num)-1] + `)`)
	untilRe = regexp.MustCompile(`(?i)Актуален:\s*([^\n▪▫]*?)(?:\s*[▪▫]|\s*Комментарий|\n|$)`)
	dateRe  = regexp.MustCompile(`(\d{1,2})\.(\d{1,2})\.(\d{2,4})`)
)

var msk = time.FixedZone("MSK", 3*3600)

func Parse(msgs []contract.Message) ([]Signal, []Unparsed) {
	var out []Signal
	var un []Unparsed
	for _, m := range msgs {
		if !strings.Contains(m.Text, "Тип сделки") {
			continue
		}
		s, reason := parseCard(m)
		if reason != "" {
			un = append(un, Unparsed{m.ID, reason})
			continue
		}
		out = append(out, s)
	}
	return out, un
}

func parseCard(m contract.Message) (Signal, string) {
	t := m.PostedAt.In(msk)
	s := Signal{PostID: m.ID, PostedAt: time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)}
	c := cardRe.FindStringSubmatch(m.Text)
	if c == nil {
		return s, "нет инструмента и контракта"
	}
	s.Asset = strings.ToUpper(c[1])
	p, ok := prefix[s.Asset]
	if !ok {
		return s, "неизвестный инструмент " + s.Asset
	}
	mon, _ := strconv.Atoi(c[2])
	if mon < 1 || mon > 12 {
		return s, "месяц контракта " + c[2]
	}
	s.SecID = p + string(months[mon-1]) + c[3][1:]

	ty := ""
	if tm := typeRe.FindStringSubmatch(m.Text); tm != nil {
		ty = strings.ToLower(tm[1])
	}
	switch {
	case strings.Contains(ty, "buy") || strings.Contains(ty, "покуп"):
		s.Long = true
	case strings.Contains(ty, "sell") || strings.Contains(ty, "прода"):
	default:
		return s, "тип сделки: " + ty
	}
	switch {
	case strings.Contains(ty, "рынк"):
		s.Kind = Market
	case strings.Contains(ty, "stop"):
		s.Kind = Stop
	default:
		s.Kind = Limit
	}
	if pm := priceRe.FindStringSubmatch(m.Text); pm != nil {
		s.Price = parseNum(pm[1])
	}
	if s.Kind != Market && s.Price <= 0 {
		return s, "нет цены"
	}
	sm := stopRe.FindStringSubmatch(m.Text)
	if sm == nil {
		return s, "нет стопа"
	}
	s.Stop = parseNum(sm[1])
	if tk := takeRe.FindStringSubmatch(m.Text); tk != nil && !strings.EqualFold(tk[1], "Открытый") {
		s.Take = parseNum(tk[1])
	}
	if um := untilRe.FindStringSubmatch(m.Text); um != nil {
		u := strings.ToLower(um[1])
		day := time.Date(s.PostedAt.Year(), s.PostedAt.Month(), s.PostedAt.Day(), 0, 0, 0, 0, time.UTC)
		switch {
		case strings.Contains(u, "сегодня"):
			s.Until = day.Add(23*time.Hour + 50*time.Minute)
		case dateRe.MatchString(u):
			dm := dateRe.FindStringSubmatch(u)
			dd, _ := strconv.Atoi(dm[1])
			mm, _ := strconv.Atoi(dm[2])
			yy, _ := strconv.Atoi(dm[3])
			if yy < 100 {
				yy += 2000
			}
			d := time.Date(yy, time.Month(mm), dd, 23, 50, 0, 0, time.UTC)
			if d.Before(s.PostedAt) {
				d = day.Add(23*time.Hour + 50*time.Minute) // дата из прошлого — опечатка, считаем днём поста
			}
			s.Until = d
		}
	}
	return s, ""
}

func parseNum(s string) float64 {
	s = strings.NewReplacer(" ", "", " ", "", ",", ".").Replace(strings.TrimSpace(s))
	s = strings.TrimRight(s, ".")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
