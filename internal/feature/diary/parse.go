// Package diary — портфель-дневник: автор публикует каждую сделку и еженедельный взнос,
// мы повторяем его портфель (Trendoman, «Идея на миллион»).
//
// В отличие от feature/ideas здесь нет шаблона: посты написаны руками, сделок в посте
// бывает несколько, суммы иногда скопированы из старого поста. Поэтому разбор терпимый,
// а каждая сделка потом сверяется с рынком (engine.go); что не разобралось или не
// сошлось — правится в data/diary/<канал>/fixes.json.
package diary

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"tg-recs-audit/internal/contract"
)

type Side string

const (
	Buy  Side = "buy"
	Sell Side = "sell"
)

// Trade — сделка, как её записал автор.
type Trade struct {
	PostID   int64     `json:"post_id"`
	PostedAt time.Time `json:"posted_at"`
	Side     Side      `json:"side"`
	Ref      string    `json:"ref"`               // как бумага названа: тикер, ISIN, номер ОФЗ, имя из синонимов
	Qty      float64   `json:"qty"`               // штук
	QtyAlt   float64   `json:"qty_alt,omitempty"` // «86 лотов (125шт)»: второе число — какое верно, решит сверка с суммой
	All      bool      `json:"all,omitempty"`     // продать всю позицию («зафиксировал позицию»)
	Amount   float64   `json:"amount"`            // руб. по словам автора (у облигаций — с НКД); 0 — не указана
	Line     string    `json:"line,omitempty"`
	Note     string    `json:"note,omitempty"` // допущение правки: «исполнение размещения считаем полным»
	// Placement — исполнение заявки на размещение по номиналу; Qty == 0 — штук = сумма / номинал.
	Placement bool `json:"placement,omitempty"`
	// Price — цена за бумагу у автора после сверки с рынком (заполняет движок). Задана в правке —
	// сделка не рыночная (оферта по номиналу): обе модели исполняют по ней, без сверки и комиссии.
	Price float64 `json:"price,omitempty"`
	Fixed bool    `json:"-"`
}

type Contribution struct {
	PostID   int64
	PostedAt time.Time
	Amount   float64
}

// Unparsed — пост с тегом сделки, из которого сделку извлечь не удалось.
type Unparsed struct {
	PostID   int64
	PostedAt time.Time
	Reason   string
	Text     string
}

// Fix — ручная правка поста: его сделки целиком (пустой список — сделок в посте нет,
// например анонс «#Покупка #Газпром» перед постом с деталями).
type Fix struct {
	PostID int64   `json:"post_id"`
	Trades []Trade `json:"trades"`
	Note   string  `json:"note"`
}

var (
	contribTag = regexp.MustCompile(`#Взнос(\d+)`)
	// Число с разрядами через пробел: «14 690,1», «3920.5». «5 11» — не 511, а 11.
	num   = `(\d{1,3}(?:[ \x{00a0}]\d{3})+(?:[.,]\d+)?|\d+(?:[.,]\d+)?)`
	numRe = regexp.MustCompile(num)
	// «р» и сразу не буква: \b в Go только для ASCII, на кириллице не срабатывает.
	rubAfter = regexp.MustCompile(`(?i)` + num + `\s*(?:р(?:$|[^\p{L}])|₽|руб)`)
	// «15 лотов (15шт)», «2 лотоа (20шт)» — количество в штуках в скобках
	qtyParen = regexp.MustCompile(`(?i)` + num + `\s*лот\S*\s*\(\s*` + num + `\s*шт`)
	// «1шт.на 3920.5р», «9ШТ. На 5564.5р», «Покупка 2000 шт»
	qtyPcs  = regexp.MustCompile(`(?i)` + num + `\s*шт`)
	qtyLots = regexp.MustCompile(`(?i)` + num + `\s*лот`)
	isinRe  = regexp.MustCompile(`\b(RU000[A-Z0-9]{7}|SU\d{5}RMFS\d|RU\d{10})\b`)
	ofzRe   = regexp.MustCompile(`(?i)(?:ОФЗ|выпуск)\s*(?:№\s*)?(2[456]\d{3})\b|#(2[456]\d{3})\b`)
	wordRe  = regexp.MustCompile(`[A-Za-z][A-Za-z0-9@]{1,6}`)
	sellRe  = regexp.MustCompile(`(?i)продал|продаж|продаю|сократил|зафиксировал`)
	buyRe   = regexp.MustCompile(`(?i)покупк|купил|покупаю|добрал|докупил|усреднил|заявк|взял`)
	// Отчёт об исполнении размещения: «Подавал заявку на 203000р, брокер насыпал на 120000р»,
	// «исполнил заявку на 100%», «исполнил заявку на ~85% (106шт)».
	allocPost = regexp.MustCompile(`(?i)первичном размещении|исполнил\S*\s+заявк|насыпал`)
	appAmount = regexp.MustCompile(`(?i)заявк\S*\s+на\s+` + num + `\s*(?:р|₽)`)
	given     = regexp.MustCompile(`(?i)насыпал\S*\s+на\s+` + num + `\s*(?:р|₽)`)
	pctRe     = regexp.MustCompile(`(?i)исполнил\S*\s+заявк\S*\s+на\s+~?(\d+)\s*%(?:\s*\(\s*` + num + `\s*шт)?`)
	// Посты, где тоже бывают сделки: реинвест купонов («направил в ОФЗ 26254, взял 3 лота»),
	// движения портфеля. В них берём только строки с глаголом сделки.
	sideTags = []string{"Купоны", "ДвиженияПортфеля", "Участвую"}
	lotWord  = regexp.MustCompile(`(?i)лот`)
	// «Общая позиция 30 шт, средняя цена 291», «…занимает долю 24.54%, средняя цена покупки … 147шт» —
	// не сделка, а размер позиции автора: сверка.
	avgRe    = regexp.MustCompile(`(?i)средн\S*\s+цен|добил до|дов[её]л до|теперь стало|стало \d`)
	parenNum = regexp.MustCompile(`\(\s*` + num + `\s*(?:р|₽|руб)?\.?\s*\)`)
)

// Holding — «Общая позиция N шт» из поста: наша позиция по бумаге обязана совпасть.
// Qty < 0 — только факт, что бумага в портфеле (список $тикеров из #БалансПортфеля).
type Holding struct {
	PostID   int64
	PostedAt time.Time
	Ref      string
	Qty      float64
}

// Snapshot — состав портфеля из #БалансПортфеля: «Активы в портфеле: $RU000A103935 $SBER …».
type Snapshot struct {
	PostID   int64
	PostedAt time.Time
	Refs     []string
}

var cashtag = regexp.MustCompile(`\$([A-Z][A-Z0-9]{2,11})\b`)

// Parse извлекает взносы и сделки из постов не раньше since.
// known — тикеры/коды бумаг; aliases — имя из поста → ссылка (тикер или ISIN).
func Parse(msgs []contract.Message, since time.Time, known map[string]bool, aliasMap map[string]string, fixes []Fix) ([]Contribution, []Trade, []Holding, []Snapshot, []Unparsed) {
	aliases := compileAliases(aliasMap)
	fixed := map[int64]Fix{}
	for _, f := range fixes {
		fixed[f.PostID] = f
	}
	var cs []Contribution
	lastContrib := map[string]time.Time{}
	var ts []Trade
	var hs []Holding
	var ss []Snapshot
	var un []Unparsed
	for _, m := range msgs {
		if m.PostedAt.Before(since) {
			continue
		}
		if n := contribTag.FindStringSubmatch(m.Text); n != nil {
			// Один номер в пределах трёх дней — один взнос: пост, разбитый на два («#Взнос19» и
			// «#Взнос19 Новая неделя…»), или репост. Через неделю тот же номер — автор забыл
			// прибавить (119, 137), взнос новый.
			if last, ok := lastContrib[n[1]]; ok && m.PostedAt.Sub(last) < 72*time.Hour {
				continue
			}
			lastContrib[n[1]] = m.PostedAt
			c := Contribution{PostID: m.ID, PostedAt: m.PostedAt, Amount: 10000}
			if r := rubAfter.FindStringSubmatch(m.Text); r != nil {
				c.Amount = parseNum(r[1])
			}
			cs = append(cs, c)
		}
		tags := map[string]bool{}
		for _, t := range m.Tags {
			tags[t] = true
		}
		if tags["БалансПортфеля"] {
			if refs := cashtag.FindAllStringSubmatch(m.Text, -1); len(refs) > 0 {
				sn := Snapshot{PostID: m.ID, PostedAt: m.PostedAt}
				for _, r := range refs {
					sn.Refs = append(sn.Refs, r[1])
				}
				ss = append(ss, sn)
			}
		}
		if f, ok := fixed[m.ID]; ok {
			for _, t := range f.Trades {
				t.PostID, t.PostedAt, t.Fixed = m.ID, m.PostedAt, t.Price > 0
				ts = append(ts, t)
			}
			continue
		}
		if a, ok := allocation(m, known, aliases); ok {
			ts = append(ts, a)
			continue
		}
		strict := false // нет #Покупка/#Продажа: только строки с глаголом сделки, не «лот» сам по себе
		if !tags["Покупка"] && !tags["Продажа"] {
			for _, t := range sideTags {
				strict = strict || tags[t]
			}
			if !strict {
				continue
			}
		}
		side := Buy
		if tags["Продажа"] && !tags["Покупка"] {
			side = Sell
		}
		got, held, reason := parsePost(m, side, strict, known, aliases)
		if strict && reason == "нет строки с количеством" {
			continue // пост про купоны без сделки — обычное дело
		}
		hs = append(hs, held...)
		if reason != "" {
			un = append(un, Unparsed{m.ID, m.PostedAt, reason, m.Text})
			continue
		}
		ts = append(ts, got...)
	}
	ts = dropApplications(ts)
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].PostedAt.Before(ts[j].PostedAt) })
	return cs, ts, hs, ss, un
}

// allocation — отчёт об исполнении размещения как сделка по номиналу.
func allocation(m contract.Message, known map[string]bool, aliases []alias) (Trade, bool) {
	if !allocPost.MatchString(m.Text) {
		return Trade{}, false
	}
	t := Trade{PostID: m.ID, PostedAt: m.PostedAt, Side: Buy, Placement: true}
	switch g, p := given.FindStringSubmatch(m.Text), pctRe.FindStringSubmatch(m.Text); {
	case g != nil:
		t.Amount, t.Line = parseNum(g[1]), g[0]
	case p != nil:
		t.Line = p[0]
		if p[2] != "" {
			t.Qty = parseNum(p[2])
		}
		if a := appAmount.FindStringSubmatch(m.Text); a != nil {
			pct, _ := strconv.ParseFloat(p[1], 64)
			t.Amount = parseNum(a[1]) * pct / 100
		}
	default:
		return Trade{}, false // «участвовал в размещении», но сколько дали — не сказано
	}
	t.Ref = singleRef(m.Text, m.Tags, known, aliases)
	if t.Ref == "" || (t.Amount == 0 && t.Qty == 0) {
		return Trade{}, false
	}
	return t, true
}

// dropApplications убирает заявки на размещение, по которым через ≤ 3 недель вышел отчёт
// об исполнении: сделка — исполнение, иначе посчитаем дважды. Заявку автор пишет и
// «заявка на покупку», и просто «покупка: 300 лотов» — но купить выпуск до размещения
// на бирже нельзя, так что любая покупка до отчёта по тому же выпуску — заявка.
// Заявка без отчёта остаётся (считаем исполненной полностью).
func dropApplications(ts []Trade) []Trade {
	var out []Trade
	for _, t := range ts {
		answered := false
		if !t.Placement && t.Side == Buy {
			for _, a := range ts {
				if a.Placement && a.Ref == t.Ref && a.PostedAt.After(t.PostedAt) && a.PostedAt.Sub(t.PostedAt) < 21*24*time.Hour {
					answered = true
				}
			}
		}
		if !answered {
			out = append(out, t)
		}
	}
	return out
}

func parsePost(m contract.Message, side Side, strict bool, known map[string]bool, aliases []alias) ([]Trade, []Holding, string) {
	lines := joinedLines(m.Text)
	postRef := singleRef(m.Text, m.Tags, known, aliases)
	var out []Trade
	var held []Holding
	for _, l := range lines {
		if avgRe.MatchString(l) {
			if q, _, _, ok := lineQty(l); ok {
				if ref := firstNonEmpty(lineRef(l, known, aliases), postRef); ref != "" {
					held = append(held, Holding{m.ID, m.PostedAt, ref, q})
				}
			}
			continue
		}
		sells, buys := sellRe.MatchString(l), buyRe.MatchString(l)
		if !sells && !buys && (strict || !lotWord.MatchString(l)) {
			continue // «добил до 100шт» — описание, а не сделка
		}
		qty, alt, rest, ok := lineQty(l)
		if !ok {
			continue
		}
		t := Trade{PostID: m.ID, PostedAt: m.PostedAt, Side: side, Qty: qty, QtyAlt: alt, Line: strings.TrimSpace(l)}
		switch {
		case sells && !buys:
			t.Side = Sell
		case buys && !sells:
			t.Side = Buy
		} // оба («с продажи ОФЗ я купил…») — сторону решает тег поста
		// Сумма: число с «р/₽» после количества («3шт (3 лота) - 11337р»), иначе в скобках
		// до количества («ФосАгро (13412,01) 2шт»), иначе первое число после количества.
		if r := rubAfter.FindStringSubmatch(rest); r != nil {
			t.Amount = parseNum(r[1])
		} else if r := parenNum.FindStringSubmatch(l); r != nil {
			t.Amount = parseNum(r[1])
		} else if r := numRe.FindStringSubmatch(rest); r != nil {
			t.Amount = parseNum(r[1])
		}
		t.Ref = lineRef(l, known, aliases)
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, held, "нет строки с количеством"
	}
	// Сделка одна, а в посте ровно один ISIN — бумага сделки он: «реализуем пополнение
	// и дивиденды от #Магнит, покупаем: АПРИ Флай…» + $RU000A107FZ5 — это не Магнит.
	// Явный $ISIN поста важнее синонима: «АйДиКоллект покупка» + $RU000A10BW88 — новый выпуск, а не старый.
	if len(out) == 1 && isinRe.MatchString(postRef) && !isinRe.MatchString(out[0].Line) {
		out[0].Ref = postRef
	}
	// Бумага поста подставляется, только если сделка в посте одна: в «Продал Магнит /
	// Тинькофф / Мать и дитя» без синонимов все три иначе стали бы продажами тега поста.
	for i := range out {
		if out[i].Ref != "" {
			continue
		}
		if len(out) > 1 || postRef == "" {
			return nil, held, "не понял, какая бумага: " + out[i].Line
		}
		out[i].Ref = postRef
	}
	return out, held, ""
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// joinedLines склеивает «Сбербанк покупка:» со следующей строкой «2 лота (20шт) 5146.99р».
func joinedLines(text string) []string {
	raw := strings.Split(text, "\n")
	var out []string
	for i := 0; i < len(raw); i++ {
		l := strings.TrimSpace(raw[i])
		for strings.HasSuffix(l, ":") && i+1 < len(raw) {
			i++
			l += " " + strings.TrimSpace(raw[i])
		}
		out = append(out, l)
	}
	return out
}

// lineQty — количество в штуках, запасное (лоты, если разошлись со штуками) и хвост строки (там сумма).
func lineQty(l string) (float64, float64, string, bool) {
	if m := qtyParen.FindStringSubmatchIndex(l); m != nil {
		pcs, lots := parseNum(l[m[4]:m[5]]), parseNum(l[m[2]:m[3]])
		if lots == pcs {
			lots = 0
		}
		return pcs, lots, l[m[1]:], true
	}
	if m := qtyPcs.FindStringSubmatchIndex(l); m != nil {
		pcs, alt := parseNum(l[m[2]:m[3]]), 0.0
		// «(166 лотов) 1660шт» — лоты в другом месте строки: запасное количество
		if lm := qtyLots.FindStringSubmatch(l); lm != nil && parseNum(lm[1]) != pcs {
			alt = parseNum(lm[1])
		}
		return pcs, alt, l[m[1]:], true
	}
	if m := qtyLots.FindStringSubmatchIndex(l); m != nil {
		// Только лоты — штуки неизвестны; лот = 1 у облигаций и у многих акций, проверит сверка с рынком.
		return parseNum(l[m[2]:m[3]]), 0, l[m[1]:], true
	}
	return 0, 0, "", false
}

func lineRef(l string, known map[string]bool, aliases []alias) string {
	if m := isinRe.FindStringSubmatch(l); m != nil {
		return m[1]
	}
	if m := ofzRe.FindStringSubmatch(l); m != nil {
		return "ОФЗ " + m[1] + m[2]
	}
	for _, w := range wordRe.FindAllString(l, -1) {
		if s := strings.TrimSuffix(strings.ToUpper(w), "@"); known[s] {
			return s
		}
	}
	return aliasIn(l, aliases)
}

// singleRef — бумага поста, если она одна: по ISIN, номеру ОФЗ, тегам или синонимам.
func singleRef(text string, tags []string, known map[string]bool, aliases []alias) string {
	set := map[string]bool{}
	for _, m := range isinRe.FindAllStringSubmatch(text, -1) {
		set[m[1]] = true
	}
	if len(set) == 0 {
		for _, m := range ofzRe.FindAllStringSubmatch(text, -1) {
			set["ОФЗ "+m[1]+m[2]] = true
		}
	}
	if len(set) == 0 {
		for _, t := range tags {
			if s := strings.TrimSuffix(strings.ToUpper(t), "@"); known[s] {
				set[s] = true
			} else if r := aliasIn(t, aliases); r != "" {
				set[r] = true
			}
		}
	}
	if len(set) == 0 {
		if r := aliasIn(text, aliases); r != "" {
			set[r] = true
		}
	}
	if len(set) != 1 {
		return ""
	}
	for r := range set {
		return r
	}
	return ""
}

// alias — синоним, скомпилированный в регэксп: «Брусника 002Р-04» находит и «Брусника 002Р - 04»,
// и «брусника 002P-04» с латинской P; целым словом, чтобы «ПИК» не цеплял «пиковые».
type alias struct {
	name string
	ref  string
	re   *regexp.Regexp
}

// Буквы, которые автор пишет то латиницей, то кириллицей.
var twins = map[rune]string{
	'p': "[pр]", 'р': "[pр]", 'o': "[oо]", 'о': "[oо]", 'c': "[cс]", 'с': "[cс]",
	'e': "[eе]", 'е': "[eе]", 'a': "[aа]", 'а': "[aа]", 'x': "[xх]", 'х': "[xх]",
	'b': "[bб]", 'б': "[bб]", 'k': "[kк]", 'к': "[kк]", 'm': "[mм]", 'м': "[mм]",
	't': "[tт]", 'т': "[tт]", 'h': "[hн]", 'н': "[hн]",
}

func compileAliases(m map[string]string) []alias {
	var out []alias
	for name, ref := range m {
		var b strings.Builder
		b.WriteString(`(?i)(?:^|[^\p{L}\p{N}])`)
		sep := false
		for _, r := range strings.ToLower(name) {
			switch {
			case r == ' ' || r == '-' || r == '–':
				if !sep {
					b.WriteString(`[\s\x{00a0}\-–]*`) // \s в Go — только ASCII, а у автора бывает неразрывный пробел
					sep = true
				}
				continue
			case twins[r] != "":
				b.WriteString(twins[r])
			default:
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
			sep = false
		}
		b.WriteString(`(?:$|[^\p{L}\p{N}])`)
		out = append(out, alias{name, ref, regexp.MustCompile(b.String())})
	}
	// Длинные раньше коротких: «Евротранс 01» важнее «Евротранс».
	sort.Slice(out, func(i, j int) bool { return len(out[i].name) > len(out[j].name) })
	return out
}

// aliasIn — синоним, который стоит в тексте раньше всех («Полипласт … покупка (деньги с продажи
// АйДиКоллект)» — бумага сделки названа первой); при равной позиции — более длинный.
func aliasIn(text string, aliases []alias) string {
	best, at := "", -1
	for _, a := range aliases { // уже по убыванию длины
		if loc := a.re.FindStringIndex(text); loc != nil && (at < 0 || loc[0] < at) {
			best, at = a.ref, loc[0]
		}
	}
	return best
}

func parseNum(s string) float64 {
	s = strings.NewReplacer(" ", "", " ", "", ",", ".").Replace(s)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
