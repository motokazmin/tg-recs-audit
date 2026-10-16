// Package smartlab — история дивидендов со страниц smart-lab.ru/q/<TICKER>/dividend/.
//
// Привилегированные акции живут на странице обыкновенных (SBERP — на /q/SBER/),
// бумаги, сменившие тикер, — иногда под новым кодом (TCSG → строки на /q/T/ с кодом T).
// Суммы — в масштабе текущей акции, см. contract.DividendRow.
package smartlab

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"tg-recs-audit/internal/contract"
)

const freshFor = 7 * 24 * time.Hour

type Client struct {
	cacheDir string
	Offline  bool
	http     *http.Client
	mu       sync.Mutex // smart-lab — сайт, а не API: по одному запросу с паузой
	pages    map[string][]contract.DividendRow
}

func New(cacheDir string) *Client {
	return &Client{cacheDir: cacheDir, http: &http.Client{Timeout: 30 * time.Second}, pages: map[string][]contract.DividendRow{}}
}

// Dividends — строки с кодом secid со страницы secid, а для XXXXP — со страницы XXXX.
func (c *Client) Dividends(secid string) ([]contract.DividendRow, error) {
	page := secid
	rows, err := c.page(page)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 && strings.HasSuffix(secid, "P") && len(secid) > 4 {
		if rows, err = c.page(strings.TrimSuffix(secid, "P")); err != nil {
			return nil, err
		}
	}
	var out []contract.DividendRow
	for _, r := range rows {
		if r.SecID == secid {
			out = append(out, r)
		}
	}
	return out, nil
}

func (c *Client) page(ticker string) ([]contract.DividendRow, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.pages[ticker]; ok {
		return r, nil
	}
	path := filepath.Join(c.cacheDir, ticker+".json")
	if st, err := os.Stat(path); err == nil && (c.Offline || time.Since(st.ModTime()) < freshFor) {
		var rows []contract.DividendRow
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &rows) == nil {
			c.pages[ticker] = rows
			return rows, nil
		}
	}
	if c.Offline {
		return nil, fmt.Errorf("%s: нет в кэше, а -offline запрещает сеть", path)
	}
	body, status, err := c.fetch("https://smart-lab.ru/q/" + ticker + "/dividend/")
	if err != nil {
		return nil, err
	}
	var rows []contract.DividendRow
	switch status {
	case http.StatusOK:
		if rows, err = Parse(body); err != nil {
			return nil, fmt.Errorf("smart-lab %s: %w", ticker, err)
		}
	case http.StatusNotFound:
		// страницы нет — выплат по этому коду не знаем
	default:
		return nil, fmt.Errorf("smart-lab %s: HTTP %d", ticker, status)
	}
	if err := os.MkdirAll(c.cacheDir, 0o755); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(rows)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return nil, err
	}
	c.pages[ticker] = rows
	time.Sleep(700 * time.Millisecond)
	return rows, nil
}

func (c *Client) fetch(u string) (string, int, error) {
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(3<<attempt) * time.Second)
		}
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
		req.Header.Set("Accept-Language", "ru")
		resp, err := c.http.Do(req)
		if err != nil {
			last = err
			continue
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			last = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		return string(b), resp.StatusCode, nil
	}
	return "", 0, fmt.Errorf("%s: %w", u, last)
}

var (
	tableRe = regexp.MustCompile(`(?s)<table[^>]*financials-param-csv-download-data="Выплаченные дивиденды[^"]*"[^>]*>(.*?)</table>`)
	rowRe   = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	cellRe  = regexp.MustCompile(`(?s)<t[dh][^>]*>(.*?)</t[dh]>`)
	tagRe   = regexp.MustCompile(`<[^>]+>`)
	dateRe  = regexp.MustCompile(`^(\d\d)\.(\d\d)\.(\d{4})$`)
)

// Parse — таблица «Выплаченные дивиденды»: тикер, дата T-1, дата отсечки, период, дивиденд.
func Parse(page string) ([]contract.DividendRow, error) {
	m := tableRe.FindStringSubmatch(page)
	if m == nil {
		return nil, nil // у бумаги не было выплат — таблицы нет
	}
	var out []contract.DividendRow
	for _, tr := range rowRe.FindAllStringSubmatch(m[1], -1) {
		var cells []string
		for _, td := range cellRe.FindAllStringSubmatch(tr[1], -1) {
			cells = append(cells, strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(td[1], ""))))
		}
		if len(cells) < 5 || !dateRe.MatchString(cells[2]) {
			continue // заголовки
		}
		if !strings.HasSuffix(cells[4], "₽") {
			return nil, fmt.Errorf("дивиденд не в рублях: %q", cells[4])
		}
		amount, err := num(strings.TrimSuffix(cells[4], "₽"))
		if err != nil {
			return nil, fmt.Errorf("строка %v: %w", cells, err)
		}
		record := iso(cells[2])
		lastBuy := iso(cells[1])
		if lastBuy == "" {
			lastBuy = record // нет T-1 — консервативно день отсечки (держать дольше, а не меньше)
		}
		out = append(out, contract.DividendRow{SecID: cells[0], LastBuyDay: lastBuy, RecordDay: record, Amount: amount})
	}
	return out, nil
}

func iso(d string) string {
	m := dateRe.FindStringSubmatch(d)
	if m == nil {
		return ""
	}
	return m[3] + "-" + m[2] + "-" + m[1]
}

func num(s string) (float64, error) {
	s = strings.NewReplacer(" ", "", " ", "", ",", ".").Replace(s)
	return strconv.ParseFloat(s, 64)
}
