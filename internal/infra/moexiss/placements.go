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

// Bonds — все облигации биржи, включая погашенные и дефолтные: без них выборка
// размещений — выжившие. Кэш на сутки.
func (c *Client) Bonds() ([]contract.Security, error) {
	path := filepath.Join(c.cacheDir, "bonds-universe.json")
	// Список пополняется новыми выпусками — сутки, не навсегда (paperd смотрит размещения вперёд).
	if st, err := os.Stat(path); !c.refresh && err == nil && (c.Offline || time.Since(st.ModTime()) < 20*time.Hour) {
		if b, err := os.ReadFile(path); err == nil {
			var out []contract.Security
			if json.Unmarshal(b, &out) == nil {
				return out, nil
			}
		}
	}
	if c.Offline {
		return nil, fmt.Errorf("%s: нет в кэше, а -offline запрещает сеть", path)
	}
	var out []contract.Security
	for start := 0; ; start += page {
		var resp struct {
			Securities issBlock `json:"securities"`
		}
		err := c.get("/securities.json", url.Values{
			"group_by": {"group"}, "group_by_filter": {"stock_bonds"},
			"limit": {strconv.Itoa(page)}, "start": {strconv.Itoa(start)},
			"securities.columns": {"secid,shortname,is_traded,type"},
		}, &resp)
		if err != nil {
			return nil, err
		}
		for _, r := range resp.Securities.Data {
			out = append(out, contract.Security{SecID: str(r[0]), ShortName: str(r[1]), IsTraded: deref(num(r[2])) == 1, Type: str(r[3])})
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

// Card — карточка бумаги (ISSUEDATE, FACEUNIT, COUPONPERCENT, …). Кэш навсегда.
func (c *Client) Card(secid string) (map[string]string, error) {
	path := filepath.Join(c.cacheDir, "cards", secid+".json")
	if b, err := os.ReadFile(path); err == nil {
		var m map[string]string
		if json.Unmarshal(b, &m) == nil {
			return m, nil
		}
	}
	if c.Offline {
		return nil, fmt.Errorf("%s: нет в кэше, а -offline запрещает сеть", path)
	}
	var resp struct {
		Description issBlock `json:"description"`
	}
	if err := c.get("/securities/"+url.PathEscape(secid)+".json", url.Values{"iss.only": {"description"}}, &resp); err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, r := range resp.Description.Data {
		m[str(r[0])] = str(r[2])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(m)
	return m, os.WriteFile(path, b, 0o644)
}

// BondHead — история облигации до till включительно (окно после размещения). Прошлое окно
// не меняется — кэш навсегда.
func (c *Client) BondHead(secid string, till time.Time) ([]contract.BondBar, error) {
	path := filepath.Join(c.cacheDir, "bondhead", secid+"_"+till.Format("20060102")+".csv")
	if bars, err := readBondBars(path); err == nil {
		return bars, nil
	}
	if c.Offline {
		return nil, fmt.Errorf("%s: нет в кэше, а -offline запрещает сеть", path)
	}
	best := map[string][]any{}
	for start := 0; ; {
		var resp struct {
			History issBlock `json:"history"`
		}
		q := url.Values{
			"till": {till.Format(time.DateOnly)}, "start": {strconv.Itoa(start)},
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
			cl = deref(num(r[6]))
		}
		bars = append(bars, contract.BondBar{Day: day, Open: deref(num(r[2])), High: deref(num(r[3])), Low: deref(num(r[4])),
			Close: cl, AccInt: deref(num(r[7])), Face: deref(num(r[8])), FaceUnit: str(r[9])})
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Day.Before(bars[j].Day) })
	return bars, writeBondBars(path, bars)
}
