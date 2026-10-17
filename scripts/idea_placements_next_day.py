"""0019: размещение по номиналу → выход по официальному закрытию первого торгового дня после
размещения. Выборка 0007 (data/analysis/placements.csv), первые дни — scripts/fetch_placement_head.py."""
import bisect, csv, json, os, sys
from collections import defaultdict
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, bars, report, summary, fmt, criterion

ld, lq = bars(os.path.join(ROOT, "data/cache/iss/shares/LQDT.csv"))
rows = {}
for r in csv.DictReader(open(os.path.join(ROOT, "data/analysis/placements.csv"))):
    if r["n"] == "20":
        rows[r["secid"]] = r

def alloc(c1):
    return 1 if c1 <= 100.5 else 0.4 if c1 <= 102 else 0.15

events, skip = [], defaultdict(int)
for secid, r in rows.items():
    if float(r["not_par"] or 0) > 0:
        skip["не по номиналу"] += 1
        continue
    d = json.load(open(os.path.join(ROOT, "data/cache/iss/plhead", secid + ".json")))
    best = {}
    for x in (dict(zip(d["columns"], v)) for v in d["history"]):
        if not x["BOARDID"].startswith("TQ") or not x["NUMTRADES"]:
            continue
        if x["TRADEDATE"] not in best or (x["VALUE"] or 0) > (best[x["TRADEDATE"]]["VALUE"] or 0):
            best[x["TRADEDATE"]] = x
    days = sorted(best)
    issue = r["issue"]
    if not days:
        skip["нет торгов"] += 1
        continue
    first = best[days[0]]
    c1 = first["CLOSE"] or first["WAPRICE"]
    k = bisect.bisect_left(ld, issue)
    if k >= len(ld) or ld[k] != issue:
        k = bisect.bisect_left(ld, issue)            # размещение в неторговый для LQDT день
    limit = ld[min(k + 3, len(ld) - 1)]
    after = [x for x in days if issue < x <= limit]
    if not after:
        skip["нет торгов 3 дня после размещения"] += 1
        continue
    x = best[after[0]]
    px = x["LEGALCLOSEPRICE"] or x["WAPRICE"] or x["CLOSE"]
    face = x["FACEVALUE"] or 1000
    ret = (px / 100 * face + (x["ACCINT"] or 0)) * (1 - COST) / face - 1
    i0 = bisect.bisect_right(ld, issue) - 1
    i1 = bisect.bisect_right(ld, after[0]) - 1
    bench = lq[ld[i1]][1] / lq[ld[i0]][1] - 1
    days_held = max(1, i1 - i0)
    events.append({"day": issue, "secid": secid, "ex": ret - bench, "per_day": (ret - bench) / days_held,
                   "phase": r["phase"], "c1": c1, "w": alloc(c1), "qual": r["qual"] == "true", "held": days_held})

print("== 0019. Размещение → официальное закрытие первого торгового дня после, против LQDT")
print("пропуски:", dict(skip))
report("ОСНОВНОЙ (полная аллокация)", events)
s = summary(events)
w = sum(e["w"] for e in events)
print(f"с аллокацией 0012 (сценарий 2): взвешенное среднее {100*sum(e['w']*e['ex'] for e in events)/w:+.2f}%")
ph = {}
for p in ("растёт", "стоит", "падает"):
    ph[p] = summary([e for e in events if e["phase"] == p])
    print(f"  ставка {p}: {fmt(ph[p])}")
for name, lo, hi in (("≤ 100.5", 0, 100.5), ("100.5–102", 100.5, 102), ("> 102", 102, 1e9)):
    print(f"  close 1-го дня {name}: {fmt(summary([e for e in events if lo < e['c1'] <= hi or (lo == 0 and e['c1'] <= hi)]))}")
print(f"  для квалов: {fmt(summary([e for e in events if e['qual']]))}")
held = sorted(e["held"] for e in events)
print(f"  дней удержания: медиана {held[len(held)//2]}")
eff1 = sum(e["per_day"] for e in events) / len(events)
eff20 = 0.0106 / 20
print(f"превышение на день удержания: N=1 {100*eff1:+.3f}%/день, N=20 (0007) {100*eff20:+.3f}%/день")
c = [("1. среднее, медиана > 0, t > 2", s["mean"] > 0 and s["median"] > 0 and s["t"] > 2),
     ("2. знак в половинах", criterion(events) or (summary([e for e in events if e['day'] < '2025'])["mean"] > 0 and summary([e for e in events if e['day'] >= '2025'])["mean"] > 0)),
     ("3. знак во всех фазах ставки", all(v.get("mean", -1) > 0 for v in ph.values())),
     ("4. эффективность выше N=20", eff1 > eff20)]
for n, ok in c:
    print(f"  {n}: {'да' if ok else 'НЕТ'}")
print("КРИТЕРИЙ:", "выполнен" if all(ok for _, ok in c) else "НЕ выполнен")
