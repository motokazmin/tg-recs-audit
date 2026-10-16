// Package contract — контракт между прикладным кодом и инфраструктурой.
//
// Leaf: зависит только от стандартной библиотеки, импортируется обеими сторонами.
package contract

import "time"

// Bar — дневной бар по основному режиму торгов.
// Цены НЕ скорректированы ни на сплиты, ни на дивиденды: так их отдаёт ISS.
type Bar struct {
	Day                    time.Time
	Open, High, Low, Close float64
	Volume                 float64
	Board                  string
}

// Security — бумага биржи, в том числе давно не торгующаяся.
type Security struct {
	SecID     string
	ShortName string
	IsTraded  bool
	Type      string
}

// Message — пост канала. Правка важна: задним числом она меняет рекомендацию.
type Message struct {
	Channel  string
	ID       int64
	PostedAt time.Time
	Edited   bool
	EditedAt *time.Time // nil, если источник знает только факт правки (веб-превью)
	Text     string
	Tags     []string // cashtag/hashtag без $/#
	Links    []string // URL из поста: у части каналов тикер — только в ссылке (investmint.ru/sibn/)
	// IDSpan — сколько ID занимает пост. Альбом из N фото — N сообщений, а виден как один пост;
	// без этого дыры в нумерации завышают число удалённых.
	IDSpan int
}

// ChannelInfo — что источник знает о канале целиком.
type ChannelInfo struct {
	Ref       string // как канал был указан пользователем
	Title     string
	Source    string // "web", "export", …
	EditTimes bool   // источник знает время правки, а не только факт (веб-превью — только факт)
}

// ChannelSource — любой способ получить историю канала: веб-превью, экспорт, MTProto.
// Прикладной код не знает, какой именно.
type ChannelSource interface {
	// Messages — все доступные посты по возрастанию ID.
	Messages() (ChannelInfo, []Message, error)
}

// PriceHistory — вся доступная история бумаги по возрастанию даты. Пусто — бумаги нет.
type PriceHistory interface {
	DailyBars(secid string) ([]Bar, error)
}

// SecurityUniverse — все акции, расписки и фонды биржи, включая делистнутые.
type SecurityUniverse interface {
	Shares() ([]Security, error)
}

// Split — сплит/консолидация: Factor новых акций за одну старую (GMKN 2024-04-08: 100).
type Split struct {
	SecID  string  `json:"secid"`
	Day    string  `json:"day"` // первый торговый день по новой цене, YYYY-MM-DD
	Factor float64 `json:"factor"`
	Note   string  `json:"note,omitempty"`
}

// Rename — бумага продолжила торговаться под другим кодом (редомициляция, обмен).
// Ratio — новых акций за одну старую.
type Rename struct {
	From  string  `json:"from"`
	To    string  `json:"to"`
	Ratio float64 `json:"ratio"`
	Note  string  `json:"note,omitempty"`
}

// Frozen — торги бумагой прекращены без преемника: позиция закрывается по последнему close.
type Frozen struct {
	SecID string `json:"secid"`
	Note  string `json:"note,omitempty"`
}

// DividendFix — ручная правка выплаты из источника: заменить ошибочную сумму или
// подтвердить ту, что не прошла сверку доходности (POSI 2024-05: две выплаты в один день).
// Amount — фактический, на акцию того времени, без пересчёта под сплиты.
type DividendFix struct {
	SecID      string  `json:"secid"`
	LastBuyDay string  `json:"last_buy_day"`
	Source     float64 `json:"source_amount,omitempty"` // какую строку источника править; 0 — единственную в этот день
	Amount     float64 `json:"amount"`
	Note       string  `json:"note,omitempty"`
}

// CorpActions — ручная таблица корпоративных действий (ISS бесплатно их не отдаёт).
type CorpActions struct {
	Splits    []Split       `json:"splits"`
	Renames   []Rename      `json:"renames"`
	Frozen    []Frozen      `json:"frozen"`
	Dividends []DividendFix `json:"dividends"`
}

// Op — операция виртуального счёта: запись журнала, как выписка брокера.
type Op struct {
	Seq      int       `json:"seq"`
	Kind     string    `json:"kind"` // buy | sell | skip
	Day      string    `json:"day"`  // торговый день исполнения, YYYY-MM-DD
	SecID    string    `json:"secid"`
	Qty      float64   `json:"qty,omitempty"`
	Price    float64   `json:"price,omitempty"`
	Amount   float64   `json:"amount,omitempty"` // руб.: покупка с комиссией, продажа за вычетом
	PostID   int64     `json:"post_id"`
	PostedAt time.Time `json:"posted_at"`
	Target   float64   `json:"target,omitempty"`
	Analyst  string    `json:"analyst,omitempty"`
	Reason   string    `json:"reason,omitempty"`
}

// Ledger — журнал операций одного портфеля. Только дописывается.
type Ledger interface {
	Load() ([]Op, error)
	Append(ops []Op) error
}

// DividendRow — выплата как её публикует источник.
//
// Amount — в масштабе ТЕКУЩЕЙ акции этого кода: источники пересчитывают старые выплаты
// под последующие сплиты (GMKN 2023: 9.153 вместо фактических 915.33). Вернуть к
// фактическому масштабу — дело прикладного кода (таблица сплитов). Цену из строки
// источника не берём: у BELU сумма пересчитана, а цена — нет.
type DividendRow struct {
	SecID      string  `json:"secid"`
	LastBuyDay string  `json:"last_buy_day"` // «дата T-1»: держать на закрытии этого дня и не продать в этот день
	RecordDay  string  `json:"record_day"`
	Amount     float64 `json:"amount"`
}

// DividendSource — история выплат по коду. Пусто — выплат нет или источник бумагу не знает.
type DividendSource interface {
	Dividends(secid string) ([]DividendRow, error)
}

// Instrument — бумага биржи, найденная по тому, как её назвали в посте.
type Instrument struct {
	SecID string `json:"secid"` // код торгов: SBER, SU26244RMFS2, RU000A10BVW6, TMON
	ISIN  string `json:"isin"`
	Name  string `json:"name"`
	Kind  string `json:"kind"` // share | bond | fund
}

// InstrumentResolver — «ОФЗ 26244», «RU000A10BVW6», «TMON» → бумага.
type InstrumentResolver interface {
	Resolve(ref string) (Instrument, error)
}

// BondBar — дневной бар облигации. Цены — в % от номинала на эту дату, AccInt — НКД
// в валюте номинала на одну бумагу. Нет сделок в день — цены нулевые, НКД есть.
type BondBar struct {
	Day                    time.Time
	Open, High, Low, Close float64
	AccInt                 float64
	Face                   float64
	FaceUnit               string
}

// BondFlow — выплата по облигации на одну бумагу, в рублях.
type BondFlow struct {
	Day        time.Time // дата выплаты
	RecordDay  time.Time // купон: держать на конец этого дня; у амортизации — пусто
	ValueRub   float64
	Amortizing bool // амортизация или погашение — номинал уменьшается
}

// BondData — история и выплаты облигаций.
type BondData interface {
	BondBars(secid string) ([]BondBar, error)
	BondFlows(secid string) ([]BondFlow, error)
}

// Rate — официальный курс валюты к рублю на день.
type Rate struct {
	Day   time.Time
	Value float64 // рублей за единицу
}

// FXRates — курсы для валютных облигаций (номинал в CNY, USD).
type FXRates interface {
	Rates(unit string) ([]Rate, error)
}
