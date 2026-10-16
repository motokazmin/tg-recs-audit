// Package moexiss — клиент MOEX ISS (без авторизации) с дисковым кэшем.
//
// Бесплатно отдаёт дневные бары всех режимов, в том числе делистнутых бумаг,
// и список бумаг вместе с делистнутыми. НЕ отдаёт дивиденды
// (/iss/cci/corp-actions/dividends — «только подписчикам») и скорректированные цены.
package moexiss

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tg-recs-audit/internal/contract"
)

const (
	base = "https://iss.moex.com/iss"
	page = 100
)

// Since — с какой даты качать историю: канал начался в 2021, запас на год.
var Since = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// freshFor — сколько кэш считается свежим без запроса хвоста.
const freshFor = 6 * time.Hour

type Client struct {
	cacheDir string
	refresh  bool
	Offline  bool // только кэш: нет файла — ошибка, а не запрос
	http     *http.Client
}

func New(cacheDir string, refresh bool) *Client {
	return &Client{cacheDir: cacheDir, refresh: refresh, http: &http.Client{Timeout: 60 * time.Second}}
}

// --- PriceHistory ---------------------------------------------------------

func (c *Client) DailyBars(secid string) ([]contract.Bar, error) {
	return c.bars("shares", secid)
}

// IndexBars — история индекса (IMOEX, MCFTR) в том же формате.
func (c *Client) IndexBars(secid string) ([]contract.Bar, error) {
	return c.bars("index", secid)
}

// Prefetch докачивает историю нескольких бумаг параллельно (ISS терпит 4 потока).
func (c *Client) Prefetch(secids []string) error {
	sem := make(chan struct{}, 4)
	errs := make(chan error, len(secids))
	var wg sync.WaitGroup
	for _, s := range secids {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if _, err := c.DailyBars(s); err != nil {
				errs <- err
			}
		}(s)
	}
	wg.Wait()
	close(errs)
	return <-errs
}

// bars: кэш на диске, при устаревании — дозагрузка хвоста с последнего бара кэша.
func (c *Client) bars(market, secid string) ([]contract.Bar, error) {
	path := filepath.Join(c.cacheDir, market, secid+".csv")
	var cached []contract.Bar
	from := Since
	if !c.refresh {
		if st, err := os.Stat(path); err == nil {
			bars, err := readBars(path)
			if err == nil && (c.Offline || time.Since(st.ModTime()) < freshFor) {
				return bars, nil
			}
			if err == nil && len(bars) > 0 {
				cached = bars
				from = bars[len(bars)-1].Day // последний бар перекачиваем: он мог быть неполным
			}
		}
	}
	if c.Offline {
		return nil, fmt.Errorf("%s: нет в кэше, а -offline запрещает сеть", path)
	}
	rows, err := c.fetchHistory(market, secid, from)
	if err != nil {
		return nil, err
	}
	fresh := pickMainBoard(rows, market == "index")
	merged := cached
	if len(fresh) > 0 {
		cut := sort.Search(len(merged), func(i int) bool { return !merged[i].Day.Before(fresh[0].Day) })
		merged = append(merged[:cut:cut], fresh...)
	}
	if err := writeBars(path, merged); err != nil {
		return nil, err
	}
	return merged, nil
}

type histRow struct {
	board, day                  string
	open, high, low, close, vol *float64
	value                       float64
}

// fetchHistory — все режимы торгов; режим дня выбирает pickMainBoard.
// Фильтр ISS marketprice_board=1 не годится: он отдаёт только нынешний основной режим —
// у ушедших с биржи бумаг (POLY, QIWI) пусто, у фондов, сменивших TQTF на TQBR
// (LQDT, TMON в 2026), история начинается с переезда.
func (c *Client) fetchHistory(market, secid string, from time.Time) ([]histRow, error) {
	var out []histRow
	for start := 0; ; {
		var resp struct {
			History issBlock `json:"history"`
		}
		q := url.Values{
			"from":            {from.Format(time.DateOnly)},
			"start":           {strconv.Itoa(start)},
			"history.columns": {"BOARDID,TRADEDATE,OPEN,HIGH,LOW,CLOSE,VOLUME,VALUE"},
		}
		err := c.get("/history/engines/stock/markets/"+market+"/securities/"+secid+".json", q, &resp)
		if err != nil {
			return nil, err
		}
		for _, r := range resp.History.Data {
			out = append(out, histRow{
				board: str(r[0]), day: str(r[1]),
				open: num(r[2]), high: num(r[3]), low: num(r[4]), close: num(r[5]), vol: num(r[6]),
				value: deref(num(r[7])),
			})
		}
		if len(resp.History.Data) < page {
			return out, nil
		}
		start += len(resp.History.Data)
	}
}

// pickMainBoard берёт на каждую дату режим с наибольшим оборотом и непустым close.
// Бумага за историю меняет режимы (EQBR → TQBR, TQBR ↔ TQDE) и в один день торгуется
// в нескольких; фиксированный TQBR потерял бы годы истории (у SBER — 2011…2013).
func pickMainBoard(rows []histRow, anyBoard bool) []contract.Bar {
	best := map[string]histRow{}
	for _, r := range rows {
		if r.close == nil || *r.close == 0 || (!anyBoard && !orderBook(r.board)) {
			continue
		}
		if b, ok := best[r.day]; !ok || r.value > b.value {
			best[r.day] = r
		}
	}
	bars := make([]contract.Bar, 0, len(best))
	for d, r := range best {
		day, err := time.Parse(time.DateOnly, d)
		if err != nil {
			continue
		}
		c := *r.close
		bars = append(bars, contract.Bar{
			Day: day, Open: orElse(r.open, c), High: orElse(r.high, c), Low: orElse(r.low, c),
			Close: c, Volume: deref(r.vol), Board: r.board,
		})
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Day.Before(bars[j].Day) })
	return bars
}

// orderBook — режим стакана (TQBR, TQTF, TQCB, EQBR…). Переговорные (PS*, RP*) и неполные
// лоты (SMAL) не берём: блок-сделка по своей цене может перевесить стакан по обороту.
func orderBook(board string) bool {
	return strings.HasPrefix(board, "TQ") || strings.HasPrefix(board, "EQ")
}

// --- SecurityUniverse -----------------------------------------------------

func (c *Client) Shares() ([]contract.Security, error) {
	path := filepath.Join(c.cacheDir, "universe.json")
	// Сутки: новые эмитенты должны попадать в разбор постов paperd.
	if st, err := os.Stat(path); !c.refresh && err == nil && (c.Offline || time.Since(st.ModTime()) < 20*time.Hour) {
		if b, err := os.ReadFile(path); err == nil {
			var out []contract.Security
			if json.Unmarshal(b, &out) == nil {
				return out, nil
			}
		}
	}
	var out []contract.Security
	for start := 0; ; start += page {
		var resp struct {
			Securities issBlock `json:"securities"`
		}
		// engine/market без is_trading отдаёт и делистнутые (YNDX, POLY есть в списке)
		err := c.get("/securities.json", url.Values{
			"engine": {"stock"}, "market": {"shares"},
			"limit": {strconv.Itoa(page)}, "start": {strconv.Itoa(start)},
			"securities.columns": {"secid,shortname,is_traded,type"},
		}, &resp)
		if err != nil {
			return nil, err
		}
		for _, r := range resp.Securities.Data {
			out = append(out, contract.Security{
				SecID: str(r[0]), ShortName: str(r[1]), IsTraded: deref(num(r[2])) == 1, Type: str(r[3]),
			})
		}
		if len(resp.Securities.Data) < page {
			break
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(out)
	return out, os.WriteFile(path, b, 0o644)
}

// --- транспорт ------------------------------------------------------------

type issBlock struct {
	Columns []string `json:"columns"`
	Data    [][]any  `json:"data"`
}

func (c *Client) get(path string, q url.Values, dst any) error {
	q.Set("iss.meta", "off")
	u := base + path + "?" + q.Encode()
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
		resp, err := c.http.Get(u)
		if err != nil {
			last = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			last = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		if err := json.Unmarshal(body, dst); err != nil {
			// ISS на закрытых эндпоинтах отвечает HTML-страницей «только подписчикам»
			return fmt.Errorf("ISS %s: не JSON: %w", u, err)
		}
		return nil
	}
	return fmt.Errorf("ISS %s: %w", u, last)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) *float64 {
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	return &f
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func orElse(p *float64, d float64) float64 {
	if p == nil || *p == 0 {
		return d
	}
	return *p
}

// --- кэш ------------------------------------------------------------------

var header = []string{"date", "open", "high", "low", "close", "volume", "board"}

func writeBars(path string, bars []contract.Bar) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write(header)
	ff := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	for _, b := range bars {
		_ = w.Write([]string{b.Day.Format(time.DateOnly), ff(b.Open), ff(b.High), ff(b.Low), ff(b.Close), ff(b.Volume), b.Board})
	}
	w.Flush()
	return w.Error()
}

func readBars(path string) ([]contract.Bar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil || len(recs) == 0 {
		return nil, fmt.Errorf("кэш %s повреждён: %v", path, err)
	}
	bars := make([]contract.Bar, 0, len(recs)-1)
	for _, r := range recs[1:] {
		day, err := time.Parse(time.DateOnly, r[0])
		if err != nil {
			return nil, err
		}
		var v [5]float64
		for i := range v {
			if v[i], err = strconv.ParseFloat(r[i+1], 64); err != nil {
				return nil, err
			}
		}
		bars = append(bars, contract.Bar{Day: day, Open: v[0], High: v[1], Low: v[2], Close: v[3], Volume: v[4], Board: r[6]})
	}
	return bars, nil
}
