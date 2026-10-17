"""0025: сигналы t.me/signals_moex «LONG #X from P р. stop loss S р.» глазами подписчика.
Вход — open первой 10-мин свечи после поста, стоп из поста, тейк 2R, срок 5 торговых дней.
Свечи ISS кэшируются в data/cache/iss/candles10/<secid>/<дата поста>.json."""
import datetime as dt, json, math, os, re, sys, time, urllib.request
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, report, summary, fmt

CACHE = os.path.join(ROOT, "data/cache/iss/candles10")
HOLD = 5
NUM = r"(\d[\d  ]*(?:[.,]\d+)?)"
RX = re.compile(r"^(LONG|SHORT)\s+#([A-Z0-9]+)\s+from\s+" + NUM + r"\s*р\.?\s*stop\s*loss\s+" + NUM, re.I)
TGT = re.compile(r"(?i)цел[ьи][^\d\n]{0,25}" + NUM)
MSK = dt.timezone(dt.timedelta(hours=3))

def num(s):
    return float(s.replace(" ", "").replace(" ", "").replace(",", "."))

def get(url):
    for i in range(6):
        try:
            with urllib.request.urlopen(url, timeout=60) as r:
                return json.load(r)
        except Exception:
            time.sleep(2 * (i + 1))
    raise RuntimeError(url)

def candles(secid, day):
    """10-мин свечи с дня поста по +12 календарных дней: [(begin, o, h, l, c)]."""
    path = os.path.join(CACHE, secid, day + ".json")
    if os.path.exists(path):
        return json.load(open(path))
    till = (dt.date.fromisoformat(day) + dt.timedelta(days=12)).isoformat()
    rows, start = [], 0
    while True:
        d = get(f"https://iss.moex.com/iss/engines/stock/markets/shares/boards/TQBR/securities/{secid}/candles.json"
                f"?interval=10&from={day}&till={till}&start={start}&iss.meta=off")["candles"]
        c = d["columns"]
        rows += [(r[c.index("begin")], r[c.index("open")], r[c.index("high")], r[c.index("low")], r[c.index("close")])
                 for r in d["data"]]
        if len(d["data"]) < 500:
            break
        start += len(d["data"])
    os.makedirs(os.path.dirname(path), exist_ok=True)
    tmp = path + f".{os.getpid()}.{id(rows)}"
    json.dump(rows, open(tmp, "w"))
    os.replace(tmp, path)                           # атомарно: читатель не увидит недописанный файл
    return rows

def parse():
    msgs = json.load(open(os.path.join(ROOT, "data/channels/signals_moex.json")))["messages"]
    out = []
    for m in msgs:
        g = RX.search(m["Text"] or "")
        if not g:
            continue
        at = dt.datetime.fromisoformat(m["PostedAt"].replace("Z", "+00:00")).astimezone(MSK).replace(tzinfo=None)
        t = TGT.search(m["Text"][g.end():])
        out.append(dict(id=m["ID"], at=at, long=g.group(1).upper() == "LONG", secid=g.group(2).upper(),
                        frm=num(g.group(3)), stop=num(g.group(4)), tgt=num(t.group(1)) if t else 0.0))
    return out

def simulate(s, bars, take_r=2.0, tgt=None):
    at = s["at"].strftime("%Y-%m-%d %H:%M:%S")
    bars = [b for b in bars if b[0] >= at]           # свеча, начавшаяся не раньше поста
    if not bars:
        return None, "нет свечей"
    sign = 1 if s["long"] else -1
    entry = bars[0][1]
    risk = sign * (entry - s["stop"])
    if risk <= 0:
        return None, "стоп за входом"
    take = tgt if tgt else entry + sign * take_r * risk
    if sign * (take - entry) <= 0:
        return None, "цель за входом"
    days = sorted({b[0][:10] for b in bars})
    last_day = days[min(HOLD, len(days)) - 1]
    if len(days) < HOLD and bars[-1][0][:10] >= (dt.date.today() - dt.timedelta(3)).isoformat():
        return None, "сделка не закрыта"
    exit_, how = None, "срок"
    for i, (b, o, h, l, c) in enumerate(bars):
        if b[:10] > last_day:
            break
        lo, hi = (l, h) if s["long"] else (-h, -l)
        st, tk, op = sign * s["stop"], sign * take, sign * o
        if i > 0 and op <= st:
            exit_, how = o, "стоп (гэп)"; break
        if i > 0 and op >= tk:
            exit_, how = o, "тейк (гэп)"; break
        if lo <= st:
            exit_, how = s["stop"], "стоп"; break
        if hi >= tk:
            exit_, how = take, "тейк"; break
        exit_ = c
    pnl = sign * (exit_ - entry) - COST * (entry + exit_)
    return dict(day=s["at"].date().isoformat(), r=pnl / risk, pct=pnl / entry, how=how, long=s["long"],
                risk_pct=risk / entry, slip=sign * (entry - s["frm"]) / risk), None

sigs = parse()
keys = sorted({(s["secid"], s["at"].date().isoformat()) for s in sigs})
with ThreadPoolExecutor(6) as ex:
    got = dict(zip(keys, ex.map(lambda k: candles(*k), keys)))
data = [got[(s["secid"], s["at"].date().isoformat())] for s in sigs]
res, skip = [], Counter()
alt = []
for s, b in zip(sigs, data):
    r, why = simulate(s, b)
    if why:
        skip[why] += 1
        continue
    res.append(r)
    if s["tgt"]:
        a, _ = simulate(s, b, tgt=s["tgt"])
        if a:
            alt.append(a)

print(f"сигналов {len(sigs)}, исполнено {len(res)}, пропуски {dict(skip)}")
report("R после издержек, тейк 2R (основное)", res, "r")
report("   LONG", [e for e in res if e["long"]], "r")
report("   SHORT", [e for e in res if not e["long"]], "r")
report("   доходность сделки, %", res, "pct")
print("исходы:", Counter(e["how"] for e in res))
print(f"стоп, медиана % цены: {100*sorted(e['risk_pct'] for e in res)[len(res)//2]:.2f}%;"
      f" вход хуже «from», медиана в R: {sorted(e['slip'] for e in res)[len(res)//2]:+.2f}")
gross = [dict(e, g=e["r"] + COST * 2 / e["risk_pct"]) for e in res]   # приближённо: издержки ≈ 2·COST·цена
print(f"до издержек (≈): {fmt(summary(gross, 'g'))}")
for lo, hi in ((0, .005), (.005, .01), (.01, .02), (.02, 1)):
    print(f"   стоп {100*lo:.1f}–{100*hi:.1f}% цены: {fmt(summary([e for e in res if lo <= e['risk_pct'] < hi], 'r'))}")
report("цель автора вместо 2R (описательно)", alt, "r")
# критерий 0025 — без медианы: при тейке 2R медиана R у любого канала около −1
halves = [summary([e for e in res if (e["day"] < "2024-01-01") == h], "r") for h in (True, False)]
legs = [summary([e for e in res if e["long"] == L], "r") for L in (True, False)]
m = summary(res, "r")
ok = m["mean"] > 0 and m["t"] > 2 and all(x["mean"] > 0 for x in halves + legs)
print("КРИТЕРИЙ 0025:", "выполнен" if ok else "НЕ выполнен")
