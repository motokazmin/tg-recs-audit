package moexiss

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"tg-recs-audit/internal/contract"
)

// Auctions — аукционы размещения ОФЗ за день (доска PACT, только дни со сделками).
// Прошедший день кэшируется навсегда: итоги аукциона не меняются; сегодня и вчера — перезапрос.
func (c *Client) Auctions(day time.Time) ([]contract.Auction, error) {
	d := day.Format(time.DateOnly)
	path := filepath.Join(c.cacheDir, "auctions", d+".json")
	final := time.Since(day) > 48*time.Hour
	if b, err := os.ReadFile(path); err == nil && (final || c.Offline) {
		var out []contract.Auction
		if json.Unmarshal(b, &out) == nil {
			return out, nil
		}
	}
	if c.Offline {
		return nil, fmt.Errorf("%s: нет в кэше, а -offline запрещает сеть", path)
	}
	var resp struct {
		History issBlock `json:"history"`
	}
	if err := c.get("/history/engines/stock/markets/bonds/boards/PACT/securities.json", url.Values{
		"date": {d}, "history.columns": {"SECID,NUMTRADES,VALUE,VOLUME,ACCINT,FACEVALUE"},
	}, &resp); err != nil {
		return nil, err
	}
	out := []contract.Auction{}
	for _, r := range resp.History.Data {
		value, volume, face := deref(num(r[2])), deref(num(r[3])), deref(num(r[5]))
		if deref(num(r[1])) == 0 || volume == 0 || face == 0 {
			continue // в день после аукциона ISS отдаёт строку с нулями
		}
		out = append(out, contract.Auction{SecID: str(r[0]), Day: day, AvgPrice: value / volume / face * 100,
			AccInt: deref(num(r[4])), Face: face, Value: value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SecID < out[j].SecID })
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(out)
	return out, os.WriteFile(path, b, 0o644)
}

// OFZBars — дневные бары ОФЗ в основном режиме (TQOB) с from. Close — официальное закрытие
// (LEGALCLOSEPRICE): CLOSE в ISS — последняя сделка с вечерней сессией (0016).
func (c *Client) OFZBars(secid string, from time.Time) ([]contract.BondBar, error) {
	path := filepath.Join(c.cacheDir, "ofz-tqob", secid+".csv")
	if st, err := os.Stat(path); err == nil && (c.Offline || time.Since(st.ModTime()) < freshFor) {
		if bars, err := readBondBars(path); err == nil {
			return bars, nil
		}
	}
	if c.Offline {
		return nil, fmt.Errorf("%s: нет в кэше, а -offline запрещает сеть", path)
	}
	var bars []contract.BondBar
	for start := 0; ; {
		var resp struct {
			History issBlock `json:"history"`
		}
		if err := c.get("/history/engines/stock/markets/bonds/boards/TQOB/securities/"+secid+".json", url.Values{
			"from": {from.Format(time.DateOnly)}, "start": {strconv.Itoa(start)},
			"history.columns": {"TRADEDATE,OPEN,LEGALCLOSEPRICE,ACCINT,FACEVALUE,NUMTRADES"},
		}, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.History.Data {
			day, err := time.Parse(time.DateOnly, str(r[0]))
			if err != nil || deref(num(r[5])) == 0 {
				continue
			}
			bars = append(bars, contract.BondBar{Day: day, Open: deref(num(r[1])), Close: deref(num(r[2])),
				AccInt: deref(num(r[3])), Face: deref(num(r[4])), FaceUnit: "RUB"})
		}
		if len(resp.History.Data) < page {
			break
		}
		start += len(resp.History.Data)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return bars, writeBondBars(path, bars)
}
