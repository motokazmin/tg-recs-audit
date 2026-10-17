"""0029: новый выпуск на вторичке — вход по официальному закрытию первого торгового дня, выход по
официальному закрытию 5-го торгового дня после, против LQDT. Корзины по закрытию дня 1."""
import bisect, csv, json, os, sys
from collections import defaultdict
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, bars, report, summary, fmt

H = 5
ld, lq = bars(os.path.join(ROOT, "data/cache/iss/shares/LQDT.csv"))
events, skip = [], defaultdict(int)
for r in csv.DictReader(open(os.path.join(ROOT, "data/analysis/placements.csv"))):
    if r["n"] != "20":
        continue
    if float(r["not_par"] or 0) > 0:
        skip["не по номиналу"] += 1
        continue
    d = json.load(open(os.path.join(ROOT, "data/cache/iss/plhead", r["secid"] + ".json")))
    best = {}
    for x in (dict(zip(d["columns"], v)) for v in d["history"]):
        if not x["BOARDID"].startswith("TQ") or not x["NUMTRADES"]:
            continue
        if x["TRADEDATE"] not in best or (x["VALUE"] or 0) > (best[x["TRADEDATE"]]["VALUE"] or 0):
            best[x["TRADEDATE"]] = x
    days = sorted(best)
    if not days:
        skip["нет торгов"] += 1
        continue
    a = best[days[0]]
    i0 = bisect.bisect_left(ld, days[0])
    if i0 + H >= len(ld) or ld[i0] != days[0]:
        skip["нет LQDT на день 1"] += 1
        continue
    target = ld[i0 + H]
    k = bisect.bisect_right(days, target) - 1
    if days[k] <= days[0] or days[k] < ld[i0 + H - 2]:
        skip["нет торгов у дня выхода"] += 1
        continue
    b = best[days[k]]
    px = lambda x: x["LEGALCLOSEPRICE"] or x["CLOSE"]
    face = a["FACEVALUE"] or 1000
    cin = (px(a) / 100 * face + (a["ACCINT"] or 0)) * (1 + COST)
    cout = (px(b) / 100 * (b["FACEVALUE"] or face) + (b["ACCINT"] or 0)) * (1 - COST)
    i1 = bisect.bisect_right(ld, days[k]) - 1
    o2 = None                                       # вход по open дня 2 (описательно)
    if len(days) > 1 and best[days[1]]["OPEN"] and days[1] < days[k]:
        x = best[days[1]]
        o2 = (x["OPEN"] / 100 * face + (x["ACCINT"] or 0)) * (1 + COST)
    events.append({"day": days[0], "secid": r["secid"], "c1": px(a),
                   "ex": cout / cin - 1 - (lq[ld[i1]][1] / lq[ld[i0]][1] - 1),
                   "ex2": cout / o2 - 1 - (lq[ld[i1]][1] / lq[ld[i0 + 1]][1] - 1) if o2 else float("nan")})

print(f"== 0029. Вторичка: закрытие дня 1 → закрытие через {H} торг. дней, против LQDT. Пропуски: {dict(skip)}")
report("все", events)
verdict = {}
for name, f in (("горячие > 100.5", lambda c: c > 100.5), ("холодные < 99.8", lambda c: c < 99.8),
                ("99.8–100.5 (описательно)", lambda c: 99.8 <= c <= 100.5)):
    g = [e for e in events if f(e["c1"])]
    print(f"\n{name}:")
    report("  ", g)
    a = summary([e for e in g if e["day"] < "2025-01-01"])
    b = summary([e for e in g if e["day"] >= "2025-01-01"])
    print(f"   2023–2024: {fmt(a)}\n   2025–2026: {fmt(b)}")
    s = summary(g)
    verdict[name] = s["n"] > 0 and s["mean"] > 0 and s["median"] > 0 and s["t"] > 2.5 and \
        a.get("mean", -1) > 0 and b.get("mean", -1) > 0
for name in list(verdict)[:2]:
    print(f"КРИТЕРИЙ 0029, {name}:", "выполнен" if verdict[name] else "НЕ выполнен")

# описательно: не снижение ли это ставки — фазы из 0007 и разница со средней корзиной той же недели
phase = {r["secid"]: r["phase"] for r in csv.DictReader(open(os.path.join(ROOT, "data/analysis/placements.csv")))}
hot = [e for e in events if e["c1"] > 100.5]
for p in ("растёт", "стоит", "падает"):
    print(f"горячие, ставка {p}: {fmt(summary([e for e in hot if phase[e['secid']] == p]))}")
from eventstats import week
mid = defaultdict(list)
for e in events:
    if 99.8 <= e["c1"] <= 100.5:
        mid[week(e["day"])].append(e["ex"])
diff = [dict(e, d=e["ex"] - sum(mid[week(e["day"])]) / len(mid[week(e["day"])])) for e in hot if mid[week(e["day"])]]
print(f"горячие минус средняя корзина той же недели: {fmt(summary(diff, 'd'))}")
for lo, hi in ((100.5, 101), (101, 102), (102, 1e9)):
    print(f"   close дня 1 {lo}–{hi}: {fmt(summary([e for e in hot if lo < e['c1'] <= hi]))}")
print(f"горячие, вход по open дня 2: {fmt(summary(hot, 'ex2'))}")
