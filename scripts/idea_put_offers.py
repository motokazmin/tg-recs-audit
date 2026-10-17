"""0014, идея 4: облигация под пут-оферту. Вход close за 40 торговых дней до оферты, выход — 100%
номинала в дату оферты, если в последние 5 торговых дней до неё был close ≥ 99, иначе последний close."""
import bisect, collections, json, os, sys
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, bars, report, criterion, summary, fmt

LEAD = 40
# Отклонение от регистрации (0015): заявки подаются в окне, которое закрывается до даты оферты,
# после окна цена уже с новым купоном — «close ≥ 99 в последние 5 дней» путает исполненные оферты.
BY_TYPE = "--by-type" in sys.argv   # исполнена, если ISS не пометил дефолт
# 0023: исполнение по торгам 60 дней после оферты на всех досках; «нет сделок» — нижняя граница
# (как дефолт, выход по последнему close до оферты) или --upper (исполнена).
BY_MARKET = "--by-market" in sys.argv
UPPER = "--upper" in sys.argv
AFTER = 60
ld, lq = bars(os.path.join(ROOT, "data/cache/iss/shares/LQDT.csv"))
C = os.path.join(ROOT, "data/cache/iss/offers")
offers = json.load(open(os.path.join(C, "offers.json")))
KEEP = {"Оферта", "Оферта (состоялось)", "(состоялось)", "Оферта (дефолт)", "Оферта (технический дефолт)"}
events, skip = [], collections.Counter()
seen = set()
for o in offers:
    day = o["offerdate"] or ""
    if not ("2021-01-01" <= day <= "2026-09-30") or o["faceunit"] not in ("RUB", "SUR"):
        continue
    if o["offertype"] not in KEEP:
        skip["тип " + str(o["offertype"])] += 1
        continue
    if o["price"] != 100:
        skip["цена оферты не 100 или нет"] += 1
        continue
    if (o["secid"], day) in seen:
        continue
    seen.add((o["secid"], day))
    path = os.path.join(C, o["secid"] + ".json")
    if not os.path.exists(path):
        skip["нет истории"] += 1
        continue
    d = json.load(open(path))
    rows = [dict(zip(d["columns"], r)) for r in d["history"]]
    sec = {r["TRADEDATE"]: r for r in rows if r["BOARDID"] == o["primary_boardid"] and r["CLOSE"]}
    days = sorted(sec)
    k = bisect.bisect_left(ld, day)
    if k - LEAD < 0 or k >= len(ld):
        skip["нет календаря"] += 1
        continue
    target = ld[k - LEAD]
    i = bisect.bisect_right(days, target) - 1
    if i < 0 or days[i] < ld[max(0, k - LEAD - 5)]:
        skip["нет сделок у дня входа"] += 1
        continue
    e = sec[days[i]]
    pre = [x for x in days if ld[max(0, k - 5)] <= x < day]
    last = [x for x in days if days[i] < x < day]
    if not last:
        skip["нет сделок до оферты"] += 1
        continue
    face_in = e["FACEVALUE"]
    cost_in = (e["CLOSE"] / 100 * face_in + (e["ACCINT"] or 0)) * (1 + COST)
    cash = sum(f["value"] or 0 for f in d["flows"] if days[i] < (f["date"] or "") <= day)
    defaulted = "дефолт" in o["offertype"]
    if BY_MARKET:
        end = ld[min(len(ld) - 1, k + AFTER)]
        post = sorted((r["TRADEDATE"], r) for r in rows if r["CLOSE"] and day < r["TRADEDATE"] <= end)
        if post and all(r["CLOSE"] >= 90 for _, r in post):
            mark = "исполнена"
        elif post:
            mark = "дефолт"
        else:
            mark = "нет сделок"
        honor = mark == "исполнена" or (mark == "нет сделок" and UPPER)
        if not honor and mark == "дефолт":
            x = post[0][1]                              # первый close после оферты, на любой доске
            fv = x["FACEVALUE"] or sec[last[-1]]["FACEVALUE"]
            out = (x["CLOSE"] / 100 * fv + (x["ACCINT"] or 0)) * (1 - COST) + \
                  sum(f["value"] or 0 for f in d["flows"] if days[i] < (f["date"] or "") <= x["TRADEDATE"])
            ret = out / cost_in - 1
            li, lo = bisect.bisect_right(ld, days[i]) - 1, bisect.bisect_right(ld, x["TRADEDATE"]) - 1
            bench = lq[ld[lo]][1] / lq[ld[li]][1] - 1
            events.append({"day": days[i], "secid": o["secid"], "px": e["CLOSE"], "ret": ret, "ex": ret - bench,
                           "honored": False, "type": o["offertype"], "name": o["name"], "mark": mark})
            continue
    if BY_MARKET and honor or (not BY_MARKET and ((not defaulted) if BY_TYPE else any(sec[x]["CLOSE"] >= 99 for x in pre))):
        out = sec[last[-1]]["FACEVALUE"] + cash          # оферта исполнена: номинал + купон/амортизация в окне
        exit_day, honored = day, True
    else:
        x = sec[last[-1]]
        out = (x["CLOSE"] / 100 * x["FACEVALUE"] + (x["ACCINT"] or 0)) * (1 - COST) + \
              sum(f["value"] or 0 for f in d["flows"] if days[i] < (f["date"] or "") <= last[-1])
        exit_day, honored = last[-1], False
    ret = out / cost_in - 1
    li, lo = bisect.bisect_right(ld, days[i]) - 1, bisect.bisect_right(ld, exit_day) - 1
    bench = lq[ld[lo]][1] / lq[ld[li]][1] - 1
    events.append({"day": days[i], "secid": o["secid"], "px": e["CLOSE"], "ret": ret, "ex": ret - bench,
                   "honored": honored, "type": o["offertype"], "name": o["name"],
                   "mark": mark if BY_MARKET else ""})

print("ПРАВИЛО ВЫХОДА:", ("по торгам после оферты (0023), нет сделок → " + ("исполнена" if UPPER else "последний close"))
      if BY_MARKET else "по типу ISS (не дефолт → 100%)" if BY_TYPE else "регистрация 0014 (close ≥ 99 за 5 дней)")
print(f"== Пут-оферты: вход close за {LEAD} торг. дней, против LQDT. Пропуски: {dict(skip)}")
below = [e for e in events if e["px"] < 99]
above = [e for e in events if e["px"] >= 99]
report("цена входа < 99% (основная)", below)
print(f"цена входа ≥ 99%: {fmt(summary(above))}")
for lo_, hi_ in ((0, 90), (90, 95), (95, 97), (97, 99)):
    print(f"   цена {lo_}–{hi_}%: {fmt(summary([e for e in below if lo_ <= e['px'] < hi_]))}")
nh = [e for e in below if not e["honored"]]
print(f"   не исполнена по правилу (последний close): {len(nh)}; типы: {collections.Counter(e['type'] for e in nh)}")
for e in sorted(below, key=lambda e: e["ex"])[:8]:
    print(f"     {e['day']} {e['secid']} {e['name'][:40]:40s} вход {e['px']:.1f} превыш. {100*e['ex']:+.1f}%")
if BY_MARKET:
    main = [e for e in below if 90 <= e["px"] < 97]
    report("ОСНОВНАЯ 0023: цена 90–97%", main)
    print("   разметка по торгам × тип ISS:", collections.Counter((e["mark"], "дефолт" in e["type"]) for e in events))
    print("   разметка в корзине < 90:", collections.Counter(e["mark"] for e in below if e["px"] < 90))
    for e in sorted(main, key=lambda e: e["ex"])[:6]:
        print(f"     {e['day']} {e['secid']} {e['name'][:40]:40s} вход {e['px']:.1f} {e['mark']:10s} превыш. {100*e['ex']:+.1f}%")
    print("КРИТЕРИЙ 0023:", "выполнен" if criterion(main) else "НЕ выполнен")
else:
    print("КРИТЕРИЙ:", "выполнен" if criterion(below) else "НЕ выполнен")
