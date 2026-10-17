"""0031: карточки идей t.me/finamalert глазами подписчика. Вход open следующего дня, стоп и цель из
карточки по дневным low/high (в один день — стоп), выход по сроку — close; против MCFTR.
Бары TQBR до 2020 — data/cache/iss/shares-ohlc/ (качаются здесь), с 2020 — кэш prefetch-shares."""
import bisect, csv, datetime as dt, json, os, re, sys, time, urllib.request
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, share, index, report, summary, fmt

OHLC = os.path.join(ROOT, "data/cache/iss/shares-ohlc")
NUM = r"(\d[\d  ]*(?:[.,]\d+)?)"
MSK = dt.timezone(dt.timedelta(hours=3))

def num(s):
    return float(s.replace(" ", "").replace(" ", "").replace(",", ".").rstrip("."))

def get(url):
    for i in range(6):
        try:
            with urllib.request.urlopen(url, timeout=60) as r:
                return json.load(r)
        except Exception:
            time.sleep(2 * (i + 1))
    raise RuntimeError(url)

def fetch(secid, frm, till):
    path = os.path.join(OHLC, f"{secid}_{frm}_{till}.json")
    if not os.path.exists(path):
        rows, start = [], 0
        while True:
            d = get(f"https://iss.moex.com/iss/history/engines/stock/markets/shares/boards/TQBR/securities/{secid}.json"
                    f"?from={frm}&till={till}&start={start}&iss.meta=off&history.columns=TRADEDATE,OPEN,HIGH,LOW,CLOSE")["history"]
            rows += d["data"]
            if len(d["data"]) < 100:
                break
            start += len(d["data"])
        os.makedirs(OHLC, exist_ok=True)
        tmp = path + ".tmp"
        json.dump(rows, open(tmp, "w"))
        os.replace(tmp, path)
    return {r[0]: (r[1] or r[4], r[2] or r[4], r[3] or r[4], r[4]) for r in json.load(open(path)) if r[4]}

def ohlc(secid):
    """{дата: (o, h, l, c)} 2018…2026."""
    out = fetch(secid, "2017-12-01", "2020-01-31")
    p = os.path.join(ROOT, "data/cache/iss/shares", secid + ".csv")
    if os.path.exists(p):
        for r in csv.DictReader(open(p)):
            if r["date"] > "2020-01-31" and float(r["close"] or 0) > 0:
                c = float(r["close"])
                out[r["date"]] = tuple(float(r[k] or 0) or c for k in ("open", "high", "low")) + (c,)
    else:
        out.update(fetch(secid, "2020-02-01", "2026-10-16"))
    return out

# --- разбор карточек
DIR = [(re.compile(r"Идея:\s*(Long|Short)", re.I), lambda g: g.lower() == "long"),
       (re.compile(r"Рекомендация\s*[-–:]\s*(покупка|продажа)", re.I), lambda g: g.lower() == "покупка"),
       (re.compile(r"(ПОКУПАТЬ|ПРОДАВАТЬ)"), lambda g: g == "ПОКУПАТЬ")]
TICK = [re.compile(r"Тикер:?\s*#?([A-Z][A-Z0-9]{2,5})\b"), re.compile(r"\(([A-Z][A-Z0-9]{2,5})\)\s*(?:ПОКУПАТЬ|ПРОДАВАТЬ)")]
TGT = re.compile(r"Цель:?\s*\$?\s*" + NUM)
STOP = re.compile(r"Стоп[ -]приказ:?\s*\$?\s*" + NUM)
HOR = re.compile(r"(?:Срок идеи|Инвестиционный горизонт|Горизонт):?\s*([^\n]{1,40})")
ENTRY = re.compile(r"Цена входа:?\s*([^\n]{1,40})")

def horizon(s):
    if not s:
        return 30
    ns = [int(x) for x in re.findall(r"\d+", s)]
    if not ns:
        return 30
    n = max(ns)
    return n * 7 if "нед" in s else n * 30 if "мес" in s else n if "дн" in s else 30

def parse():
    msgs = json.load(open(os.path.join(ROOT, "data/channels/finamalert.json")))["messages"]
    out, seen = [], {}
    for m in msgs:
        t = m["Text"] or ""
        if "pinned" in t[:80]:
            continue
        sec = next((g.group(1) for r in TICK if (g := r.search(t))), None)
        d = next((f(g.group(1)) for r, f in DIR if (g := r.search(t))), None)
        tg, st = TGT.search(t), STOP.search(t)
        if not (sec and d is not None and tg and st):
            continue
        at = dt.datetime.fromisoformat(m["PostedAt"].replace("Z", "+00:00")).astimezone(MSK)
        key = (sec, d, num(tg.group(1)))
        if key in seen and (at - seen[key]).days <= 7:
            continue
        seen[key] = at
        h, e = HOR.search(t), ENTRY.search(t)
        cond = bool(e and re.search(r"(?i)выше|ниже|достижени|пробо|закр", e.group(1)))
        out.append(dict(id=m["ID"], day=at.date().isoformat(), secid=sec, long=d, tgt=num(tg.group(1)),
                        stop=num(st.group(1)), hold=horizon(h.group(1) if h else ""), cond=cond))
    return out

def mcftr_closes():
    e = json.load(open(os.path.join(ROOT, "data/cache/iss/shares-early/MCFTR.json")))
    out = {r[0]: r[1] for r in e if r[1]}
    md, mc = index("MCFTR")
    out.update({d: mc[d][1] for d in md})
    return sorted(out), out

ideas = parse()
secids = sorted({i["secid"] for i in ideas})
with ThreadPoolExecutor(6) as ex:
    B = dict(zip(secids, ex.map(ohlc, secids)))
md, mc = mcftr_closes()

res, skip = [], Counter()
for s in ideas:
    b = B[s["secid"]]
    days = sorted(b)
    i = bisect.bisect_right(days, s["day"])
    if not days or i >= len(days) or days[i] > (dt.date.fromisoformat(s["day"]) + dt.timedelta(7)).isoformat():
        skip["нет на TQBR"] += 1
        continue
    sign = 1 if s["long"] else -1
    entry = b[days[i]][0]
    if sign * (s["tgt"] - entry) <= 0 or sign * (entry - s["stop"]) <= 0:
        skip["цель или стоп за входом"] += 1
        continue
    if not (0.2 < s["tgt"] / entry < 5 and 0.2 < s["stop"] / entry < 5):
        skip["цель не в масштабе цены"] += 1
        continue
    end = (dt.date.fromisoformat(days[i]) + dt.timedelta(s["hold"])).isoformat()
    if end > days[-1]:
        skip["срок не истёк"] += 1
        continue
    win = [b[d][3] for d in days[max(0, i - 1):] if d <= end]
    if any(not (1 / 3 < y / x < 3) for x, y in zip(win, win[1:])):
        skip["скачок цены (сплит?) в окне"] += 1
        continue
    exit_, how, k = None, "срок", i
    for k in range(i, len(days)):
        if days[k] > end:
            k -= 1
            break
        o, h, l, c = b[days[k]]
        if k > i and sign * o <= sign * s["stop"]:
            exit_, how = o, "стоп (гэп)"; break
        if k > i and sign * o >= sign * s["tgt"]:
            exit_, how = o, "цель (гэп)"; break
        if (l if s["long"] else -h) <= sign * s["stop"]:
            exit_, how = s["stop"], "стоп"; break
        if (h if s["long"] else -l) >= sign * s["tgt"]:
            exit_, how = s["tgt"], "цель"; break
    if exit_ is None:
        exit_ = b[days[k]][3]
    ret = sign * (exit_ - entry) / entry - 2 * COST
    j0, j1 = bisect.bisect_left(md, days[i]) - 1, bisect.bisect_right(md, days[k]) - 1
    if j0 < 0:
        skip["нет MCFTR"] += 1
        continue
    bench = mc[md[j1]] / mc[md[j0]] - 1
    res.append(dict(s, day=days[i], ret=ret, ex=ret - sign * bench, how=how,
                    r=ret / ((sign * (entry - s["stop"])) / entry)))

print(f"карточек {len(ideas)}, исполнено {len(res)}, пропуски {dict(skip)}")
ok = True
for name, lo, hi in (("A 2018–2021", "2018", "2022"), ("B 2022–2026", "2022", "2027")):
    L = [e for e in res if e["long"] and lo <= e["day"] < hi]
    S = [e for e in res if not e["long"] and lo <= e["day"] < hi]
    print(f"\n== {name}")
    report("Long, превышение над MCFTR", L)
    print(f"   Long, доходность сделки: {fmt(summary(L, 'ret'))}")
    print(f"   Long, в R: {fmt(summary(L, 'r'))}")
    print(f"   Long, вход по рынку / условный: {fmt(summary([e for e in L if not e['cond']]))} | {fmt(summary([e for e in L if e['cond']]))}")
    print(f"   Short (описательно): {fmt(summary(S))}")
    print("   исходы Long:", Counter(e["how"] for e in L))
    s = summary(L)
    ok &= s["n"] > 0 and s["mean"] > 0 and s["t"] > 2 and s["median"] > 0
print("\nКРИТЕРИЙ 0031:", "выполнен" if ok else "НЕ выполнен")
if "--worst" in sys.argv:
    for e in sorted(res, key=lambda e: -abs(e["ret"]))[:8]:
        print(e["day"], e["secid"], "long" if e["long"] else "short", e["how"], f"{100*e['ret']:+.1f}%", e["tgt"], e["stop"])
