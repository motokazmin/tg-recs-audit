"""0014, идеи 1 и 3: весь дивидендный календарь smart-lab.
1 — правило M: вход open за 29 торговых дней до T-1, выход close дня отсечки + дивиденд; с постом
divonline и без. 3 — гэп: вход close дня отсечки, выход close через 20 (5, 10) торговых дней."""
import bisect, csv, datetime as dt, glob, json, math, os, statistics as st, sys
from collections import defaultdict
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, share, index, summary, fmt, report, criterion, week

LEAD = 29
md, mc = index("MCFTR")

def mcftr(d0, d1, open0=True):
    i, j = bisect.bisect_left(md, d0), bisect.bisect_right(md, d1) - 1
    if i >= len(md) or j < 0:
        return float("nan")
    a = mc[md[i]][0] if (open0 and md[i] == d0) else mc[md[i]][1]
    return mc[md[j]][1] / a - 1

# выплаты: строки со всех страниц, по (бумага, T-1)
rows = {}
for p in glob.glob(os.path.join(ROOT, "data/cache/smartlab/*.json")):
    for r in json.load(open(p)) or []:
        if r.get("last_buy_day") and r.get("amount"):
            rows[(r["secid"], r["last_buy_day"])] = r["amount"]

# посты divonline: (бумага, T-1)
posts = defaultdict(list)
for r in csv.DictReader(open(os.path.join(ROOT, "data/analysis/divevents-divonline.csv"))):
    posts[r["secid"]].append(dt.date.fromisoformat(r["last_buy"]))

def covered(secid, lb):
    d = dt.date.fromisoformat(lb)
    return any(abs((x - d).days) <= 7 for x in posts.get(secid, []))

def splits(days, b):
    """[(дата, множитель)]: суммы smart-lab — в масштабе текущей акции, до сплита умножаем."""
    out = []
    for a, c in zip(days, days[1:]):
        r = b[a][1] / b[c][1]
        if r >= 5:
            out.append((c, round(r)))
        elif r <= 0.2:
            out.append((c, 1 / round(1 / r)))
    return out

M, G = [], {5: [], 10: [], 20: []}
skipped = defaultdict(int)
cache = {}
for (secid, lb), amount in sorted(rows.items()):
    if not ("2021-01-01" <= lb <= "2026-09-30"):
        continue
    if secid not in cache:
        days, b = share(secid)
        cache[secid] = (days, b, splits(days, b))
    days, b, sp = cache[secid]
    if not days:
        skipped["нет баров ISS"] += 1
        continue
    k = bisect.bisect_right(days, lb) - 1           # T-1 или последний торговый день до него
    if k < 0 or k + 1 >= len(days) or days[k] < (dt.date.fromisoformat(lb) - dt.timedelta(7)).isoformat():
        skipped["нет торгов у T-1"] += 1
        continue
    ex_i = k + 1                                    # день отсечки
    div = amount
    for d, f in sp:
        if lb < d:
            div *= f
    if any(days[max(0, k - LEAD)] < d <= days[min(len(days) - 1, ex_i + 20)] for d, _ in sp):
        skipped["сплит в окне"] += 1
        continue
    pxT1 = b[days[k]][1]
    y = div / pxT1
    if y > 0.5:
        skipped["доходность > 50% — сбой масштаба"] += 1
        continue
    vol = 0
    cov = covered(secid, lb)
    if k - LEAD >= 0:
        e = days[k - LEAD]
        ret = (b[days[ex_i]][1] * (1 - COST) + div) / (b[e][0] * (1 + COST)) - 1
        M.append({"day": e, "secid": secid, "lb": lb, "ex": ret - mcftr(e, days[ex_i]), "cov": cov, "y": y})
    for h in G:
        if ex_i + h < len(days):
            d0, d1 = days[ex_i], days[ex_i + h]
            ret = b[d1][1] * (1 - COST) / (b[d0][1] * (1 + COST)) - 1
            G[h].append({"day": d0, "secid": secid, "ex": ret - mcftr(d0, d1, open0=False), "y": y, "cov": cov})

print("Выплат smart-lab с T-1 2021…2026-09:", sum(1 for (s, lb) in rows if "2021-01-01" <= lb <= "2026-09-30"),
      "пропуски:", dict(skipped))

def weekly(evs):
    w = defaultdict(list)
    for e in evs:
        w[week(e["day"])].append(e["ex"])
    return [st.mean(v) for v in w.values()]

print(f"\n== Идея 1. Правило M: open за {LEAD} торг. дней до T-1 → close дня отсечки + дивиденд, против MCFTR")
cov = [e for e in M if e["cov"]]
unc = [e for e in M if not e["cov"]]
print(f"покрытие divonline: {len(cov)} из {len(M)} ({100*len(cov)/len(M):.0f}%)")
report("все выплаты", M)
report("с постом divonline", cov)
report("без поста", unc)
a, b_ = weekly(cov), weekly(unc)
t = (st.mean(a) - st.mean(b_)) / math.sqrt(st.variance(a) / len(a) + st.variance(b_) / len(b_))
print(f"разность «с постом − без»: {100*(st.mean(a)-st.mean(b_)):+.2f}% по неделям, t={t:+.2f}")
print("КРИТЕРИЙ «канал отбирает»:", "выполнен" if t > 2 else "НЕ выполнен")
for lo, hi in ((0, 0.03), (0.03, 0.07), (0.07, 1)):
    print(f"   доходность {100*lo:.0f}–{100*hi:.0f}%: с постом {fmt(summary([e for e in cov if lo <= e['y'] < hi]))}")
    print(f"   {'':17s} без поста {fmt(summary([e for e in unc if lo <= e['y'] < hi]))}")

print("\n== Идея 3. Гэп: close дня отсечки → close через N торг. дней, против MCFTR")
for h in (5, 10):
    print(f"N={h}: {fmt(summary(G[h]))}")
g = G[20]
report("N=20 (основной)", g)
ys = sorted(g, key=lambda e: e["y"])
n = len(ys) // 3
same = 0
main = summary(g)["mean"]
for name, part in (("низкая", ys[:n]), ("средняя", ys[n:2*n]), ("высокая", ys[2*n:])):
    s = summary(part)
    same += (s["mean"] > 0) == (main > 0)
    print(f"   доходность {name}: {fmt(s)}")
print(f"   с постом divonline: {fmt(summary([e for e in g if e['cov']]))}")
print("КРИТЕРИЙ:", "выполнен" if criterion(g) and same >= 2 else "НЕ выполнен")
