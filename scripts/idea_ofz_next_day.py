"""0016: аукцион ОФЗ — вход по средневзвешенной цене аукциона (PACT VALUE/VOLUME), выход по
официальному закрытию TQOB следующего дня; контроль — покупка на TQOB по закрытию дня аукциона."""
import bisect, glob, json, math, os, statistics as st, sys
from collections import defaultdict
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, bars, report, summary, fmt, week, tstat

# Доска аукциона: PACT с 2023 (регистрация 0016); AUCT — 2020–2023, отложенная выборка (0017).
BOARD = sys.argv[1] if len(sys.argv) > 1 else "PACT"
ld, lq = bars(os.path.join(ROOT, "data/cache/iss/shares/LQDT.csv"))

def lqdt(d0, d1):
    return lq[ld[bisect.bisect_right(ld, d1) - 1]][1] / lq[ld[bisect.bisect_right(ld, d0) - 1]][1] - 1

def full(px, r):
    return px / 100 * r["FACEVALUE"] + (r["ACCINT"] or 0)

events, skip = [], defaultdict(int)
for path in sorted(glob.glob(os.path.join(ROOT, "data/cache/iss/ofz2/*.json"))):
    d = json.load(open(path))
    secid = os.path.basename(path)[:-5]
    rows = [dict(zip(d["columns"], r)) for r in d["history"]]
    sec = {r["TRADEDATE"]: r for r in rows if r["BOARDID"] == "TQOB" and r["NUMTRADES"]}
    days = sorted(sec)
    coupons = [(c["date"], c["value"] or 0) for c in d["coupons"]]
    for r in rows:
        if r["BOARDID"] != BOARD or not r["NUMTRADES"] or not r["VOLUME"]:
            continue
        day = r["TRADEDATE"]
        if not ("2020-01-01" <= day <= "2026-09-30"):
            continue
        i = bisect.bisect_left(days, day)
        if i + 1 >= len(days) or days[i] != day:
            skip["нет торгов TQOB в день аукциона или после"] += 1
            continue
        a, n = sec[day], sec[days[i + 1]]
        if not a["LEGALCLOSEPRICE"] or not n["LEGALCLOSEPRICE"]:
            skip["нет официального закрытия"] += 1
            continue
        face = r["FACEVALUE"] or a["FACEVALUE"]
        avg = r["VALUE"] / r["VOLUME"] / face * 100            # средневзвешенная аукциона, % номинала
        acc = r["ACCINT"] if r["ACCINT"] is not None else a["ACCINT"]
        cin = (avg / 100 * face + (acc or 0)) * (1 + COST)
        cpn = sum(v for cd, v in coupons if day < cd <= n["TRADEDATE"])
        bench = lqdt(day, n["TRADEDATE"])
        def leg(px_out, base=cin):
            return (full(px_out, n) * (1 - COST) + cpn) / base - 1 - bench
        main = leg(n["LEGALCLOSEPRICE"])
        wap = leg(n["WAPRICE"]) if n["WAPRICE"] else float("nan")
        opn = leg(n["OPEN"]) if n["OPEN"] else float("nan")
        ctrl = leg(n["LEGALCLOSEPRICE"], full(a["LEGALCLOSEPRICE"], a) * (1 + COST))
        kind = {"26": "ПД", "25": "ПД", "29": "ПК", "24": "ПК", "52": "ИН"}.get(secid[2:4], secid[2:4])
        events.append({"day": day, "secid": secid, "kind": kind, "ex": main, "wap": wap, "open": opn,
                       "ctrl": ctrl, "prem": main - ctrl, "conc": (a["LEGALCLOSEPRICE"] - avg) / 100,
                       "cut": (avg - r["LOW"]) / 100 if r["LOW"] else float("nan"), "value": r["VALUE"]})

print(f"== 0016. Аукционы ОФЗ ({BOARD}): вход средневзвешенная аукциона, выход официальное закрытие TQOB след. дня, против LQDT")
print("пропуски:", dict(skip))
report("ОСНОВНОЙ", events)
report("премия аукциона (основной − контроль)", events, "prem")
print(f"контроль (покупка на TQOB по закрытию дня аукциона): {fmt(summary(events, 'ctrl'))}")
print(f"выход по WAPRICE след. дня: {fmt(summary(events, 'wap'))}")
print(f"выход по OPEN след. дня:    {fmt(summary(events, 'open'))}")
print(f"уступка к офиц. закрытию дня аукциона: {fmt(summary(events, 'conc'))}")
print(f"средневзвешенная выше отсечения на:   {fmt(summary(events, 'cut'))}")
for k in ("ПД", "ПК", "ИН"):
    print(f"  {k}: {fmt(summary([e for e in events if e['kind'] == k]))}")
by = sorted(events, key=lambda e: e["value"])
n3 = len(by) // 3
for name, part in (("малые", by[:n3]), ("средние", by[n3:2*n3]), ("крупные", by[2*n3:])):
    print(f"  объём {name}: {fmt(summary(part))}")
for y in range(2020, 2027):
    print(f"  {y}: {fmt(summary([e for e in events if e['day'].startswith(str(y))]))}")
s, a, b, p = summary(events), summary([e for e in events if e["day"] < "2024"]), summary([e for e in events if e["day"] >= "2024"]), summary(events, "prem")
crit = [("1. среднее и медиана > 0", s["mean"] > 0 and s["median"] > 0), ("2. t > 2", s["t"] > 2),
        ("3. знак в половинах", a.get("mean", 1) > 0 and b.get("mean", 1) > 0), ("4. премия аукциона > 0, t > 2", p["mean"] > 0 and p["t"] > 2)]
for name, ok in crit:
    print(f"  {name}: {'да' if ok else 'НЕТ'}")
print("КРИТЕРИЙ:", "выполнен" if all(ok for _, ok in crit) else "НЕ выполнен")
