// Package ideas — разбор постов канала в инвест-идеи.
//
// Шаблоны investmint (@ruforecasts), оба живые в истории:
//
//	до 2026:  Инвестидея по акции Сбербанк #SBER
//	          • Целевая цена: 3 540 ₽ (+12,1%)
//	          • Дата идеи: 9 января 2024
//	          • Аналитики: Finam Alert
//	с 2026:   Инвестидея по акции МТС-Банк
//	          Т-Инвестиции, идея от 9 октября 2026:
//	          • Цена идеи: 935,5 ₽
//	          • Целевая цена: 1 250 ₽
//	          #MBNK
//
// «Прогноз по акции …» — то же с «Прогнозная цена» и иногда «Прогноз: Покупать/Держать».
// Пост, который похож на идею, но не разобрался, — не молча пропускается, а
// возвращается с причиной: доля отказов видна в отчёте.
package ideas

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"tg-recs-audit/internal/contract"
)

type Idea struct {
	PostID   int64
	PostedAt time.Time
	SecID    string
	Kind     string // "идея" | "прогноз"
	Target   float64
	IdeaDate time.Time // дата идеи у аналитика; раньше поста в медиане на 9 дней
	Analyst  string
	Rating   string // «Покупать», «Держать», …; пусто — не указан
}

// Reject — пост с заголовком идеи, который не превратился в идею.
type Reject struct {
	PostID int64
	Reason string
}

var (
	header  = regexp.MustCompile(`^(Инвест-?идея|Прогноз) по акци`)
	hashtag = regexp.MustCompile(`#([A-Za-z][A-Za-z0-9]{1,5})\b`)
	// «Цена:» без уточнения — самые ранние посты (апрель 2021), там это тоже цель
	target   = regexp.MustCompile(`(?:Целевая цена|Прогнозная цена|• Цена):[ \x{00a0}]*([\d \x{00a0}\x{202f},.]+)(₽|\$|€)?`)
	ideaDate = regexp.MustCompile(`(?:Дата(?: идеи)?:|идея от)\s*(\d{1,2})\s+([а-я]+)\s+(\d{4})`)
	analyst  = regexp.MustCompile(`Аналитики:\s*([^\n]+)`)
	analyst2 = regexp.MustCompile(`(?m)^([^\n•#]+?), идея от `)
	rating   = regexp.MustCompile(`Прогноз:\s*([А-Яа-я ]+)`)
)

var months = map[string]time.Month{
	"января": 1, "февраля": 2, "марта": 3, "апреля": 4, "мая": 5, "июня": 6,
	"июля": 7, "августа": 8, "сентября": 9, "октября": 10, "ноября": 11, "декабря": 12,
}

// Parse разбирает посты. known — коды бумаг биржи: хэштег вне списка тикером не считается.
func Parse(msgs []contract.Message, known map[string]bool) ([]Idea, []Reject) {
	var out []Idea
	var rej []Reject
	for _, m := range msgs {
		h := header.FindStringSubmatch(m.Text)
		if h == nil {
			continue // реклама, «Ближайшие отсечки» и прочее — не идеи
		}
		idea := Idea{PostID: m.ID, PostedAt: m.PostedAt, Kind: "идея"}
		if h[1] == "Прогноз" {
			idea.Kind = "прогноз"
		}
		for _, t := range hashtag.FindAllStringSubmatch(m.Text, -1) {
			if s := strings.ToUpper(t[1]); known[s] {
				idea.SecID = s
				break
			}
		}
		if idea.SecID == "" {
			rej = append(rej, Reject{m.ID, "нет тикера биржи в хэштегах"})
			continue
		}
		tp := target.FindStringSubmatch(m.Text)
		if tp == nil {
			rej = append(rej, Reject{m.ID, "нет целевой цены"})
			continue
		}
		if tp[2] == "$" || tp[2] == "€" {
			rej = append(rej, Reject{m.ID, "цель не в рублях: " + tp[2]})
			continue
		}
		v, ok := parseNumber(tp[1])
		if !ok || v <= 0 {
			rej = append(rej, Reject{m.ID, "целевая цена не число: " + strings.TrimSpace(tp[1])})
			continue
		}
		idea.Target = v
		if d := ideaDate.FindStringSubmatch(m.Text); d != nil {
			if mon, ok := months[d[2]]; ok {
				day, _ := strconv.Atoi(d[1])
				year, _ := strconv.Atoi(d[3])
				idea.IdeaDate = time.Date(year, mon, day, 0, 0, 0, 0, time.UTC)
			}
		}
		if a := analyst.FindStringSubmatch(m.Text); a != nil {
			idea.Analyst = strings.TrimSpace(a[1])
		} else if a := analyst2.FindStringSubmatch(m.Text); a != nil {
			idea.Analyst = strings.TrimSpace(a[1])
		}
		if r := rating.FindStringSubmatch(m.Text); r != nil {
			idea.Rating = strings.TrimSpace(r[1])
		}
		out = append(out, idea)
	}
	return out, rej
}

// parseNumber: «3 540», «772,67», «1 250» (неразрывный пробел) → число.
func parseNumber(s string) (float64, bool) {
	s = strings.NewReplacer(" ", "", " ", "", " ", "", ",", ".").Replace(strings.TrimSpace(s))
	s = strings.TrimRight(s, ".")
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}
