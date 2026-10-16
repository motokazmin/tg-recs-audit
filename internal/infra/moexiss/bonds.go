package moexiss

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tg-recs-audit/internal/contract"
)

// --- InstrumentResolver ---------------------------------------------------

// Resolve: «ОФЗ 26244» → SU26244RMFS2, ISIN → код торгов, тикер → сам тикер. Кэш навсегда:
// соответствие кода и ISIN не меняется.
func (c *Client) Resolve(ref string) (contract.Instrument, error) {
	path := filepath.Join(c.cacheDir, "resolve.json")
	cache := map[string]contract.Instrument{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &cache)
	}
	if in, ok := cache[ref]; ok {
		return in, nil
	}
	if c.Offline {
		return contract.Instrument{}, fmt.Errorf("%q: нет в кэше %s, а -offline запрещает сеть", ref, path)
	}
	q, wantOFZ := ref, false
	if strings.HasPrefix(ref, "ОФЗ ") {
		q, wantOFZ = strings.TrimPrefix(ref, "ОФЗ "), true
	}
	var resp struct {
		Securities issBlock `json:"securities"`
	}
	if err := c.get("/securities.json", url.Values{"q": {q}, "securities.columns": {"secid,isin,shortname,type,group"}}, &resp); err != nil {
		return contract.Instrument{}, err
	}
	var found []contract.Instrument
	for _, r := range resp.Securities.Data {
		secid, isin, name, typ, group := str(r[0]), str(r[1]), str(r[2]), str(r[3]), str(r[4])
		kind := kindOf(group)
		if kind == "" {
			continue
		}
		match := secid == ref || isin == ref
		if wantOFZ {
			match = typ == "ofz_bond" && strings.HasPrefix(secid, "SU"+q)
		}
		if match {
			found = append(found, contract.Instrument{SecID: secid, ISIN: isin, Name: name, Kind: kind})
		}
	}
	if len(found) == 0 && !wantOFZ {
		// Поиск не отдаёт бумаги, сменившие код (TCSG после перехода в T), а карточка — есть.
		if in, ok, err := c.card(ref); err != nil {
			return in, err
		} else if ok {
			found = append(found, in)
		}
	}
	if len(found) != 1 {
		return contract.Instrument{}, fmt.Errorf("%q: в ISS найдено %d подходящих бумаг — нужен синоним или правка", ref, len(found))
	}
	cache[ref] = found[0]
	if err := os.MkdirAll(c.cacheDir, 0o755); err != nil {
		return found[0], err
	}
	b, _ := json.Marshal(cache)
	return found[0], os.WriteFile(path, b, 0o644)
}

func kindOf(group string) string {
	switch group {
	case "stock_bonds":
		return "bond"
	case "stock_shares", "stock_dr":
		return "share"
	case "stock_ppif", "stock_etf":
		return "fund"
	}
	return ""
}

// card — бумага по карточке /iss/securities/<код> (поиск её может не отдавать).
func (c *Client) card(secid string) (contract.Instrument, bool, error) {
	d, err := c.Card(secid)
	if err != nil {
		return contract.Instrument{}, false, err
	}
	kind := kindOf(d["GROUP"])
	if d["SECID"] != secid || kind == "" {
		return contract.Instrument{}, false, nil
	}
	return contract.Instrument{SecID: secid, ISIN: d["ISIN"], Name: d["SHORTNAME"], Kind: kind}, true, nil
}

// --- BondData -------------------------------------------------------------

func (c *Client) BondBars(secid string) ([]contract.BondBar, error) {
	path := filepath.Join(c.cacheDir, "bonds", secid+".csv")
	if st, err := os.Stat(path); err == nil && (c.Offline || time.Since(st.ModTime()) < freshFor) {
		if bars, err := readBondBars(path); err == nil {
			return bars, nil
		}
	}
	if c.Offline {
		return nil, fmt.Errorf("%s: нет в кэше, а -offline запрещает сеть", path)
	}
	// Облигаций у автора — десятки, история короткая: качаем целиком, все режимы (см. fetchHistory).
	best := map[string][]any{}
	for start := 0; ; {
		var resp struct {
			History issBlock `json:"history"`
		}
		q := url.Values{
			"start":           {strconv.Itoa(start)},
			"history.columns": {"BOARDID,TRADEDATE,OPEN,HIGH,LOW,CLOSE,WAPRICE,ACCINT,FACEVALUE,FACEUNIT,VALUE"},
		}
		if err := c.get("/history/engines/stock/markets/bonds/securities/"+secid+".json", q, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.History.Data {
			d := str(r[1])
			if !orderBook(str(r[0])) {
				continue
			}
			if b, ok := best[d]; !ok || deref(num(r[10])) > deref(num(b[10])) {
				best[d] = r
			}
		}
		if len(resp.History.Data) < page {
			break
		}
		start += len(resp.History.Data)
	}
	bars := make([]contract.BondBar, 0, len(best))
	for d, r := range best {
		day, err := time.Parse(time.DateOnly, d)
		if err != nil {
			continue
		}
		cl := deref(num(r[5]))
		if cl == 0 {
			cl = deref(num(r[6])) // нет close — средневзвешенная
		}
		bars = append(bars, contract.BondBar{
			Day: day, Open: deref(num(r[2])), High: deref(num(r[3])), Low: deref(num(r[4])), Close: cl,
			AccInt: deref(num(r[7])), Face: deref(num(r[8])), FaceUnit: str(r[9]),
		})
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Day.Before(bars[j].Day) })
	return bars, writeBondBars(path, bars)
}

func (c *Client) BondFlows(secid string) ([]contract.BondFlow, error) {
	path := filepath.Join(c.cacheDir, "bondization", secid+".json")
	if st, err := os.Stat(path); err == nil && (c.Offline || time.Since(st.ModTime()) < 7*24*time.Hour) {
		var flows []contract.BondFlow
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &flows) == nil {
			return flows, nil
		}
	}
	if c.Offline {
		return nil, fmt.Errorf("%s: нет в кэше, а -offline запрещает сеть", path)
	}
	var flows []contract.BondFlow
	for _, part := range []string{"coupons", "amortizations"} {
		for start := 0; ; start += 100 {
			var resp map[string]issBlock
			cols := "coupondate,recorddate,value_rub"
			if part == "amortizations" {
				cols = "amortdate,amortdate,value_rub"
			}
			if err := c.get("/securities/"+secid+"/bondization.json", url.Values{
				"iss.only": {part}, "limit": {"100"}, "start": {strconv.Itoa(start)}, part + ".columns": {cols},
			}, &resp); err != nil {
				return nil, err
			}
			blk := resp[part]
			for _, r := range blk.Data {
				day, err := time.Parse(time.DateOnly, str(r[0]))
				if err != nil {
					continue
				}
				f := contract.BondFlow{Day: day, ValueRub: deref(num(r[2])), Amortizing: part == "amortizations"}
				if rd, err := time.Parse(time.DateOnly, str(r[1])); err == nil && part == "coupons" {
					f.RecordDay = rd
				}
				flows = append(flows, f)
			}
			if len(blk.Data) < 100 {
				break
			}
		}
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].Day.Before(flows[j].Day) })
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(flows)
	return flows, os.WriteFile(path, b, 0o644)
}

func writeBondBars(path string, bars []contract.BondBar) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"date", "open", "high", "low", "close", "accint", "face", "faceunit"})
	ff := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	for _, b := range bars {
		_ = w.Write([]string{b.Day.Format(time.DateOnly), ff(b.Open), ff(b.High), ff(b.Low), ff(b.Close), ff(b.AccInt), ff(b.Face), b.FaceUnit})
	}
	w.Flush()
	return w.Error()
}

func readBondBars(path string) ([]contract.BondBar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil || len(recs) == 0 {
		return nil, fmt.Errorf("кэш %s повреждён: %v", path, err)
	}
	out := make([]contract.BondBar, 0, len(recs)-1)
	for _, r := range recs[1:] {
		day, err := time.Parse(time.DateOnly, r[0])
		if err != nil {
			return nil, err
		}
		var v [6]float64
		for i := range v {
			if v[i], err = strconv.ParseFloat(r[i+1], 64); err != nil {
				return nil, err
			}
		}
		out = append(out, contract.BondBar{Day: day, Open: v[0], High: v[1], Low: v[2], Close: v[3], AccInt: v[4], Face: v[5], FaceUnit: r[7]})
	}
	return out, nil
}
