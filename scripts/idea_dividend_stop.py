"""0021: стоп-лосс на событиях divonline (идея 10). Без стопа — правило B из 0011; со стопом —
close ≤ вход·(1−X) с дня входа по T-1 → продажа по open следующего дня, деньги в MCFTR до отсечки."""
import bisect, csv, math, os, sys
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, share, index, report, summary, fmt

md, mc = index("MCFTR")

def mcftr(d, open_):
    i = bisect.bisect_right(md, d) - 1
    if i < 0:
        return float("nan")
    return mc[md[i]][0] if (open_ and md[i] == d) else mc[md[i]][1]

events, cache = [], {}
for r in csv.DictReader(open(os.path.join(ROOT, "data/analysis/divevents-divonline.csv"))):
    if r["skip"]:
        continue
    s = r["secid"]
    if s not in cache:
        cache[s] = share(s)
    days, b = cache[s]
    i = bisect.bisect_left(days, r["entry_day"])
    a = bisect.bisect_right(days, r["last_buy"]) - 1
    ex = a + 1
    div = float(r["div"]) if r["paid"] == "true" else 0.0
    events.append(dict(secid=s, day=r["entry_day"], days=days, b=b, i=i, a=a, ex=ex, div=div,
                       ret_b=float(r["ret_b"]), mcftr_b=float(r["mcftr_b"])))

def run(X, relative=False):
    out, hits = [], 0
    for e in events:
        days, b, i, a, ex, div = e["days"], e["b"], e["i"], e["a"], e["ex"], e["div"]
        inp = b[days[i]][0] * (1 + COST)
        m0 = mcftr(days[i], True)
        mfull = mcftr(days[ex], False) / m0 - 1
        base = (b[days[ex]][1] * (1 - COST) + div) / inp - 1
        stop = None
        for k in range(i, a + 1):
            c = b[days[k]][1]
            if relative:
                hit = (c / b[days[i]][0]) / (mcftr(days[k], False) / m0) <= 1 - X
            else:
                hit = c <= b[days[i]][0] * (1 - X)
            if hit:
                stop = k + 1
                break
        if stop is None:
            alt = base
        else:
            hits += 1
            got = b[days[stop]][0] * (1 - COST) + (div if stop == ex else 0)  # продал в день отсечки — дивиденд есть
            # после выхода — MCFTR от open дня выхода до close дня отсечки
            alt = (got / inp) * (mcftr(days[ex], False) / mcftr(days[stop], True)) - 1
        out.append(dict(day=e["day"], base=base - mfull, alt=alt - mfull, diff=alt - base))
    return out, hits

ev, _ = run(1.0)
chk = max(abs((e["base"]) - (x["ret_b"] - x["mcftr_b"])) for e, x in zip(ev, events))
print(f"событий {len(events)}; сверка с 0011 (без стопа): макс. расхождение {chk:.2e}")
report("без стопа, превышение над MCFTR", ev, "base")
print()
for rel in (False, True):
    for X in (0.05, 0.10, 0.15):
        ev, hits = run(X, rel)
        name = ("относительный" if rel else "стоп") + f" −{int(X*100)}%"
        print(f"== {name}: сработал {hits} ({100*hits/len(ev):.0f}%)")
        report("   со стопом, превышение", ev, "alt")
        report("   разность со стопом − без", ev, "diff")
        print()

# хвост: ради него стоп и ставят
def q(xs, p):
    xs = sorted(xs)
    return xs[int(p * (len(xs) - 1))]
ev, _ = run(1.0)
print("хвост превышения над MCFTR: худшее / 1% / 5%")
print(f"   без стопа:  {100*min(e['base'] for e in ev):+6.1f}% / {100*q([e['base'] for e in ev], .01):+6.1f}% / {100*q([e['base'] for e in ev], .05):+6.1f}%")
for X in (0.05, 0.10, 0.15):
    ev, _ = run(X)
    xs = [e["alt"] for e in ev]
    print(f"   стоп −{int(X*100):2d}%: {100*min(xs):+6.1f}% / {100*q(xs, .01):+6.1f}% / {100*q(xs, .05):+6.1f}%")
