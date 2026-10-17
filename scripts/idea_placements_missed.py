"""0034: правило 0007 (по номиналу в день размещения → через N торговых дней, против LQDT) на выпусках,
которые отбор 0006 потерял: погашенные обычные облигации, у которых ISS убрал купон из карточки.
Считается как `placements.Evaluate`; история 45 календарных дней и купоны — data/cache/iss/plhead45/."""
import bisect, json, os, sys
from concurrent.futures import ThreadPoolExecutor
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, bars, report, summary, fmt
from placements_universe import select, get

C = os.path.join(ROOT, "data/cache/iss/plhead45")
MODE = next((a.split("=", 1)[1] for a in sys.argv if a.startswith("--matured=")), "only")
EXIT_COST = 0.0005

def head(row):
    secid, issue = row
    path = os.path.join(C, secid + ".json")
    if not os.path.exists(path):
        import datetime as dt
        till = (dt.date.fromisoformat(issue) + dt.timedelta(45)).isoformat()
        h, start = [], 0
        while True:
            page = json.loads(get(f"https://iss.moex.com/iss/history/engines/stock/markets/bonds/securities/{secid}.json"
                                  f"?from={issue}&till={till}&iss.meta=off&start={start}"
                                  "&history.columns=TRADEDATE,BOARDID,VALUE,OPEN,CLOSE,WAPRICE,ACCINT,FACEVALUE"))["history"]["data"]
            h += page
            if len(page) < 100:
                break
            start += len(page)
        b = json.loads(get(f"https://iss.moex.com/iss/securities/{secid}/bondization.json?iss.meta=off&limit=unlimited&iss.only=coupons"))["coupons"]
        cps = [(r[b["columns"].index("coupondate")], r[b["columns"].index("value_rub")] or 0) for r in b["data"]]
        os.makedirs(C, exist_ok=True)
        tmp = path + ".tmp"
        json.dump({"history": h, "coupons": cps}, open(tmp, "w"))
        os.replace(tmp, path)
    return json.load(open(path))

ld, lq = bars(os.path.join(ROOT, "data/cache/iss/shares/LQDT.csv"))
sel, skip = select("2023-01-01", "2026-07-15", MODE)
with ThreadPoolExecutor(6) as ex:
    data = list(ex.map(head, sel))
res = {5: [], 20: []}
for (sid, issue), d in zip(sel, data):
    best = {}
    for r in d["history"]:
        if not (r[1].startswith("TQ") or r[1].startswith("EQ")):
            continue
        if r[0] not in best or (r[2] or 0) > (best[r[0]][2] or 0):
            best[r[0]] = r
    days = sorted(best)
    i0 = bisect.bisect_left(ld, issue)
    if i0 + 20 >= len(ld):
        continue
    if days:
        f = best[days[0]]
        px = f[3] or f[4]
        if px and not (97 <= px <= 103):
            skip["не по номиналу"] += 1
            continue
    face = (best[days[0]][7] if days else 0) or 1000
    for n in res:
        ex_day = ld[i0 + n]
        last = [best[x] for x in days if issue < x <= ex_day and (best[x][4] or best[x][5])]
        if not last:
            skip[f"нет торгов {n}"] += 1
            continue
        L = last[-1]
        cl = L[4] or L[5]
        value = cl * L[7] / 100 + (L[6] or 0) + (face - L[7]) + sum(v for dd, v in d["coupons"] if dd and issue < dd <= ex_day)
        ret = value * (1 - EXIT_COST) / face - 1
        res[n].append({"day": issue, "ex": ret - (lq[ld[i0 + n]][1] / lq[ld[i0]][1] - 1)})

print(f"== 0034, режим отбора «{MODE or 'как 0006'}»: выпусков {len(sel)}, пропуски {dict(skip)}")
for n in (20, 5):
    report(f"N={n}, превышение над LQDT", res[n])
