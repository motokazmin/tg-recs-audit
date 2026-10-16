// Package cbr — официальные курсы ЦБ (XML_dynamic). Биржевой USD с июня 2024 не торгуется
// (в ISS CLOSE = 0), поэтому для всех валют берём курс ЦБ — единообразно.
package cbr

import (
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tg-recs-audit/internal/contract"
)

var codes = map[string]string{"USD": "R01235", "CNY": "R01375", "EUR": "R01239"}

// From — начало истории курсов.
var From = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

type Client struct {
	cacheDir string
	Offline  bool
	http     *http.Client
}

func New(cacheDir string) *Client {
	return &Client{cacheDir: cacheDir, http: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) Rates(unit string) ([]contract.Rate, error) {
	code, ok := codes[unit]
	if !ok {
		return nil, fmt.Errorf("курс %s: валюта не поддержана", unit)
	}
	path := filepath.Join(c.cacheDir, unit+".csv")
	if st, err := os.Stat(path); err == nil && (c.Offline || time.Since(st.ModTime()) < 12*time.Hour) {
		if r, err := read(path); err == nil {
			return r, nil
		}
	}
	if c.Offline {
		return nil, fmt.Errorf("%s: нет в кэше, а -offline запрещает сеть", path)
	}
	u := fmt.Sprintf("https://www.cbr.ru/scripts/XML_dynamic.asp?date_req1=%s&date_req2=%s&VAL_NM_RQ=%s",
		From.Format("02/01/2006"), time.Now().Format("02/01/2006"), code)
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36") // без него ЦБ отвечает 403
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ЦБ %s: HTTP %d", unit, resp.StatusCode)
	}
	var doc struct {
		Records []struct {
			Date  string `xml:"Date,attr"`
			Value string `xml:"VunitRate"`
		} `xml:"Record"`
	}
	dec := xml.NewDecoder(resp.Body)
	// Ответ в windows-1251, но нужные поля — цифры и точки: кодировку не перекодируем.
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("ЦБ %s: %w", unit, err)
	}
	var out []contract.Rate
	for _, r := range doc.Records {
		d, err1 := time.Parse("02.01.2006", r.Date)
		v, err2 := strconv.ParseFloat(strings.ReplaceAll(r.Value, ",", "."), 64)
		if err1 == nil && err2 == nil {
			out = append(out, contract.Rate{Day: d, Value: v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out, write(path, out)
}

func write(path string, rs []contract.Rate) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	for _, r := range rs {
		_ = w.Write([]string{r.Day.Format(time.DateOnly), strconv.FormatFloat(r.Value, 'f', -1, 64)})
	}
	w.Flush()
	return w.Error()
}

func read(path string) ([]contract.Rate, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	out := make([]contract.Rate, 0, len(recs))
	for _, r := range recs {
		d, err := time.Parse(time.DateOnly, r[0])
		if err != nil {
			return nil, err
		}
		v, err := strconv.ParseFloat(r[1], 64)
		if err != nil {
			return nil, err
		}
		out = append(out, contract.Rate{Day: d, Value: v})
	}
	return out, nil
}
