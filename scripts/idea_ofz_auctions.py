"""0014, идея 2: покупка ОФЗ на аукционе Минфина (доска PACT), выход close TQOB через 5 дней."""
import bisect, glob, json, math, os, sys
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, bars, report, criterion, summary, fmt

HOLD = int(sys.argv[1]) if len(sys.argv) > 1 else 5
ld, lq = bars(os.path.join(ROOT, "data/cache/iss/shares/LQDT.csv"))
events = []
for path in sorted(glob.glob(os.path.join(ROOT, "data/cache/iss/ofz/*.json"))):
    d = json.load(open(path))
    secid = os.path.basename(path)[:-5]
    rows = [dict(zip(d["columns"], r)) for r in d["history"]]
    sec = {r["TRADEDATE"]: r for r in rows if r["BOARDID"] == "TQOB" and r["CLOSE"]}
    days = sorted(sec)
    coupons = [(c["date"], c["value"] or 0) for c in d["coupons"]]
    for r in rows:
        if r["BOARDID"] != "PACT" or not r["CLOSE"] or not r["NUMTRADES"]:
            continue
        day = r["TRADEDATE"]
        if not ("2020-01-01" <= day <= "2026-09-30"):
            continue
        i = bisect.bisect_left(days, day)
        if i + HOLD >= len(days) or days[i] != day:
            continue
        ex = sec[days[i + HOLD]]
        face = r["FACEVALUE"] or sec[day]["FACEVALUE"]
        cost_in = (r["CLOSE"] / 100 * face + (r["ACCINT"] or sec[day]["ACCINT"] or 0)) * (1 + COST)
        cpn = sum(v for cd, v in coupons if day < cd <= ex["TRADEDATE"])
        out = (ex["CLOSE"] / 100 * ex["FACEVALUE"] + (ex["ACCINT"] or 0)) * (1 - COST) + cpn
        ret = out / cost_in - 1
        li, lo = bisect.bisect_right(ld, day) - 1, bisect.bisect_right(ld, ex["TRADEDATE"]) - 1
        bench = lq[ld[lo]][1] / lq[ld[li]][1] - 1
        kind = {"26": "ПД", "29": "ПК", "52": "ИН", "25": "ПД", "24": "ПК"}.get(secid[2:4], secid[2:4])
        events.append({"day": day, "secid": secid, "kind": kind, "ret": ret, "ex": ret - bench,
                       "conc": (sec[day]["CLOSE"] - r["CLOSE"]) / 100, "value": r["VALUE"]})

print(f"== Аукционы ОФЗ: вход по цене PACT, выход close TQOB через {HOLD} торг. дней, против LQDT")
report("превышение", events)
report("уступка (close TQOB в день аукциона − цена PACT, п.п./100)", events, "conc")
for k in ("ПД", "ПК", "ИН"):
    print(f"  {k}: {fmt(summary([e for e in events if e['kind'] == k]))}")
big = sorted(events, key=lambda e: e["value"])
n = len(big) // 3
for name, part in (("малые", big[:n]), ("средние", big[n:2*n]), ("крупные", big[2*n:])):
    print(f"  объём {name}: {fmt(summary(part))}")
print("КРИТЕРИЙ:", "выполнен" if criterion(events) and summary(events, "conc")["mean"] > 0 and summary(events, "conc")["t"] > 2 else "НЕ выполнен")
