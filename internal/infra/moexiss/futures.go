package moexiss

import (
	"encoding/csv"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"tg-recs-audit/internal/contract"
)

// FuturesBars — 10-минутные свечи фьючерса FORTS за один день (время — московское, в зоне UTC,
// как и дни постов). Коды контрактов переиспользуются раз в 10 лет (MXM1 — и 2021, и 2031),
// поэтому запрос всегда с датой. Прошедший день — кэш навсегда, сегодняшний — не кэшируем.
func (c *Client) FuturesBars(secid string, day time.Time) ([]contract.Bar, error) {
	path := filepath.Join(c.cacheDir, "fut", secid, day.Format(time.DateOnly)+".csv")
	if b, err := readFutBars(path); err == nil {
		return b, nil
	}
	if c.Offline {
		return nil, fmt.Errorf("%s: нет в кэше, а -offline запрещает сеть", path)
	}
	var out []contract.Bar
	for start := 0; ; {
		var resp struct {
			Candles issBlock `json:"candles"`
		}
		d := day.Format(time.DateOnly)
		if err := c.get("/engines/futures/markets/forts/securities/"+secid+"/candles.json", url.Values{
			"interval": {"10"}, "from": {d}, "till": {d}, "start": {strconv.Itoa(start)},
		}, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Candles.Data {
			t, err := time.Parse(time.DateTime, str(r[6]))
			if err != nil {
				continue
			}
			out = append(out, contract.Bar{Day: t, Open: deref(num(r[0])), Close: deref(num(r[1])),
				High: deref(num(r[2])), Low: deref(num(r[3])), Volume: deref(num(r[5]))})
		}
		if len(resp.Candles.Data) < 500 {
			break
		}
		start += len(resp.Candles.Data)
	}
	msk := time.Now().In(time.FixedZone("MSK", 3*3600))
	if day.Format(time.DateOnly) < msk.Format(time.DateOnly) {
		if err := writeFutBars(path, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func writeFutBars(path string, bars []contract.Bar) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	ff := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	for _, b := range bars {
		_ = w.Write([]string{b.Day.Format(time.DateTime), ff(b.Open), ff(b.High), ff(b.Low), ff(b.Close), ff(b.Volume)})
	}
	w.Flush()
	return w.Error()
}

func readFutBars(path string) ([]contract.Bar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	out := make([]contract.Bar, 0, len(recs))
	for _, r := range recs {
		t, err := time.Parse(time.DateTime, r[0])
		if err != nil {
			return nil, err
		}
		var v [5]float64
		for i := range v {
			if v[i], err = strconv.ParseFloat(r[i+1], 64); err != nil {
				return nil, err
			}
		}
		out = append(out, contract.Bar{Day: t, Open: v[0], High: v[1], Low: v[2], Close: v[3], Volume: v[4]})
	}
	return out, nil
}
