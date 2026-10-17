"""0033: правило 0029 на размещениях 2021–2022 (отложенная выборка). Отбор 0006 повторяет
placements.Universe; бенчмарк — LQDT с 2022-07-22, раньше ключевая ставка ЦБ.
--matured=with|only (0034): у погашенных обычных выпусков ISS убирает купон из карточки, и отбор 0006
выкидывает их вместе с нотами; with — вернуть их, only — только они. --from/--to — окно ISSUEDATE."""
import bisect, datetime as dt, json, os, sys
from concurrent.futures import ThreadPoolExecutor
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, bars, index, report, summary, fmt
from fetch_placement_head import fetch as fetch_head, CACHE as HEAD
from placements_universe import select, keyrate

H = 5
arg = lambda k, d: next((a.split("=", 1)[1] for a in sys.argv if a.startswith(f"--{k}=")), d)
FROM, TO, MATURED = arg("from", "2021-01-01"), arg("to", "2022-12-31"), arg("matured", "")
sel, skip = select(FROM, TO, MATURED)
with ThreadPoolExecutor(6) as ex:
    list(ex.map(fetch_head, sel))

# календарь и бенчмарк
e = json.load(open(os.path.join(ROOT, "data/cache/iss/shares-early/MCFTR.json")))
cal = sorted({r[0] for r in e if r[1]} | set(index("MCFTR")[0]))
ld, lq = bars(os.path.join(ROOT, "data/cache/iss/shares/LQDT.csv"))
kd, kr = keyrate()

def bench(d0, d1):
    if d0 >= ld[0]:
        i0, i1 = bisect.bisect_right(ld, d0) - 1, bisect.bisect_right(ld, d1) - 1
        return lq[ld[i1]][1] / lq[ld[i0]][1] - 1
    a, b = dt.date.fromisoformat(d0), dt.date.fromisoformat(d1)
    g = 1.0
    while a < b:
        g *= 1 + kr[max(0, bisect.bisect_right(kd, a.isoformat()) - 1)] / 365
        a += dt.timedelta(1)
    return g - 1

events = []
for sid, issue in sel:
    d = json.load(open(os.path.join(HEAD, sid + ".json")))
    best = {}
    for x in (dict(zip(d["columns"], v)) for v in d["history"]):
        if not x["BOARDID"].startswith("TQ") or not x["NUMTRADES"]:
            continue
        if x["TRADEDATE"] not in best or (x["VALUE"] or 0) > (best[x["TRADEDATE"]]["VALUE"] or 0):
            best[x["TRADEDATE"]] = x
    days = sorted(best)
    if not days:
        skip["нет торгов"] += 1; continue
    a = best[days[0]]
    p0 = a["OPEN"] or a["CLOSE"]
    if p0 and not (97 <= p0 <= 103):
        skip["не по номиналу"] += 1; continue
    i0 = bisect.bisect_left(cal, days[0])
    if i0 + H >= len(cal) or cal[i0] != days[0]:
        skip["нет дня 1 в календаре"] += 1; continue
    k = bisect.bisect_right(days, cal[i0 + H]) - 1
    if days[k] <= days[0] or days[k] < cal[i0 + H - 2]:
        skip["нет торгов у дня выхода"] += 1; continue
    b = best[days[k]]
    px = lambda x: x["LEGALCLOSEPRICE"] or x["CLOSE"]
    face = a["FACEVALUE"] or 1000
    cin = (px(a) / 100 * face + (a["ACCINT"] or 0)) * (1 + COST)
    cout = (px(b) / 100 * (b["FACEVALUE"] or face) + (b["ACCINT"] or 0)) * (1 - COST)
    events.append({"day": days[0], "secid": sid, "c1": px(a), "ex": cout / cin - 1 - bench(days[0], days[k])})

print(f"== {FROM}…{TO}, погашенные без купона в карточке: {MATURED or 'нет (как 0006)'}. По отбору {len(sel)}, событий {len(events)}, пропуски {dict(skip)}")
hot = [e for e in events if e["c1"] > 100.5]
report("все", events)
print(f"ГОРЯЧИЕ > 100.5: {fmt(summary(hot))}")
for y in ("2021", "2022"):
    print(f"   {y}: {fmt(summary([e for e in hot if e['day'][:4] == y]))}")
print(f"холодные < 99.8: {fmt(summary([e for e in events if e['c1'] < 99.8]))}")
print(f"99.8–100.5: {fmt(summary([e for e in events if 99.8 <= e['c1'] <= 100.5]))}")
s = summary(hot)
print("КРИТЕРИЙ 0033:", "выполнен" if s["n"] and s["mean"] > 0 and s["median"] > 0 and s["t"] > 2 else "НЕ выполнен")
