"""0027: шорт после дивидендной отсечки, хедж MCFTR. Вход close дня отсечки, выход close через
5 (10, 20) торговых дней; плата за шорт 15% годовых. Отложенная выборка — T-1 2017…2020,
бары до 2021 — в data/cache/iss/shares-early/ (качаются здесь)."""
import bisect, datetime as dt, glob, json, os, statistics as st, sys, time, urllib.request
from collections import defaultdict, Counter
from concurrent.futures import ThreadPoolExecutor
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, share, index, report, summary, fmt, criterion

EARLY = os.path.join(ROOT, "data/cache/iss/shares-early")
FEE = float(next((a.split("=")[1] for a in sys.argv if a.startswith("--fee=")), "0.15"))

def get(url):
    for i in range(6):
        try:
            with urllib.request.urlopen(url, timeout=60) as r:
                return json.load(r)
        except Exception:
            time.sleep(2 * (i + 1))
    raise RuntimeError(url)

def early(secid, market="shares", board="boards/TQBR/"):
    """Дневные бары 2016-12…2021-02: (даты, {дата: (close, оборот ₽)})."""
    path = os.path.join(EARLY, secid + ".json")
    if not os.path.exists(path):
        rows, start = [], 0
        while True:
            d = get(f"https://iss.moex.com/iss/history/engines/stock/markets/{market}/{board}securities/{secid}.json"
                    f"?from=2016-12-01&till=2021-02-28&start={start}&iss.meta=off"
                    "&history.columns=TRADEDATE,CLOSE,VALUE")["history"]
            rows += d["data"]
            if len(d["data"]) < 100:
                break
            start += len(d["data"])
        os.makedirs(EARLY, exist_ok=True)
        tmp = path + ".tmp" + secid
        json.dump(rows, open(tmp, "w"))
        os.replace(tmp, path)
    out = {r[0]: (r[1], r[2] or 0) for r in json.load(open(path)) if r[1]}
    return sorted(out), out

def late(secid):
    days, b = share(secid)
    vol = {}
    p = os.path.join(ROOT, "data/cache/iss/shares", secid + ".csv")
    if os.path.exists(p):
        import csv
        for r in csv.DictReader(open(p)):
            vol[r["date"]] = float(r["volume"] or 0) * float(r["close"] or 0)
    return days, {d: (b[d][1], vol.get(d, 0)) for d in days}

rows = {}
for p in glob.glob(os.path.join(ROOT, "data/cache/smartlab/*.json")):
    for r in json.load(open(p)) or []:
        if r.get("last_buy_day") and r.get("amount"):
            rows[(r["secid"], r["last_buy_day"])] = r["amount"]

def run(lo, hi, src, mdays, mc):
    ev, skip = defaultdict(list), Counter()
    secids = sorted({s for (s, lb) in rows if lo <= lb <= hi})
    with ThreadPoolExecutor(6) as ex:
        bars = dict(zip(secids, ex.map(src, secids)))
    for (secid, lb), amount in sorted(rows.items()):
        if not (lo <= lb <= hi):
            continue
        days, b = bars[secid]
        if not days:
            skip["нет баров"] += 1
            continue
        k = bisect.bisect_right(days, lb) - 1
        if k < 20 or k + 21 >= len(days) or days[k] < (dt.date.fromisoformat(lb) - dt.timedelta(7)).isoformat():
            skip["нет торгов у T-1 или окна"] += 1
            continue
        ex_i = k + 1
        win = [b[days[j]][0] for j in range(k - 20, ex_i + 21)]
        if any(not (1 / 3 < y / x < 3) for x, y in zip(win, win[1:])):
            skip["скачок цены (сплит?)"] += 1
            continue
        turn = st.median(b[days[j]][1] for j in range(k - 20, k))
        y = amount / b[days[k]][0]
        if y > 0.5:
            skip["доходность > 50%"] += 1
            continue
        for h in (5, 10, 20):
            d0, d1 = days[ex_i], days[ex_i + h]
            i0, i1 = bisect.bisect_right(mdays, d0) - 1, bisect.bisect_right(mdays, d1) - 1
            if i0 < 0 or mdays[i0] != d0:
                skip[f"нет MCFTR {h}"] += 1
                continue
            stock = b[d1][0] / b[d0][0] - 1
            idx = mc[mdays[i1]] / mc[mdays[i0]] - 1
            cal = (dt.date.fromisoformat(d1) - dt.date.fromisoformat(d0)).days
            ex = -stock + idx - 2 * COST - FEE * cal / 365
            ev[h].append({"day": d0, "secid": secid, "ex": ex, "liq": turn >= 100e6, "y": y})
    return ev, skip

# MCFTR: ранний кусок из ISS, поздний — кэш index
d = early("MCFTR", "index", "")
md_e, mc_e = d[0], {k: v[0] for k, v in d[1].items()}
md_l, mcl = index("MCFTR")
mc_l = {k: v[1] for k, v in mcl.items()}

print(f"плата за шорт {100*FEE:.0f}% годовых, хедж MCFTR, издержки {COST*1e4:.0f} б.п. на ногу\n")
ev, skip = run("2017-01-01", "2020-12-31", early, md_e, mc_e)
print("== ОТЛОЖЕННАЯ 2017–2020, пропуски:", dict(skip))
main = [e for e in ev[5] if e["liq"]]
print(f"ликвидных {len(main)} из {len(ev[5])}")
report("ОСНОВНАЯ: ликвидные, 5 дней", main)
a = [e for e in main if e["day"] < "2019-01-01"]
b = [e for e in main if e["day"] >= "2019-01-01"]
print(f"   2017–2018: {fmt(summary(a))}")
print(f"   2019–2020: {fmt(summary(b))}")
for h in (10, 20):
    print(f"   {h} дней, ликвидные: {fmt(summary([e for e in ev[h] if e['liq']]))}")
print(f"   5 дней, неликвидные: {fmt(summary([e for e in ev[5] if not e['liq']]))}")
sm = summary(main)
ok = sm["n"] > 0 and sm["mean"] > 0 and sm["median"] > 0 and sm["t"] > 2 and \
    summary(a).get("mean", -1) > 0 and summary(b).get("mean", -1) > 0
print("КРИТЕРИЙ 0027:", "выполнен" if ok else "НЕ выполнен")

ev2, skip2 = run("2021-01-01", "2026-09-30", late, md_l, mc_l)
print("\n== 2021–2026 (видели в 0015, описательно), пропуски:", dict(skip2))
report("ликвидные, 5 дней", [e for e in ev2[5] if e["liq"]])
print(f"   все, 5 дней: {fmt(summary(ev2[5]))}")
