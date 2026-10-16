"""Общая статистика событийных проверок (0014): превышение, t по неделям, половины."""
import csv, datetime as dt, math, os, statistics as st
from collections import defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
COST = 0.0005

def bars(path):
    """CSV кэша ISS → (даты, {дата: (open, close)}) по возрастанию."""
    out = {}
    for r in csv.DictReader(open(path)):
        c = float(r["close"] or 0)
        if c > 0:
            out[r["date"]] = (float(r["open"] or 0) or c, c)
    return sorted(out), out

def share(secid):
    p = os.path.join(ROOT, "data/cache/iss/shares", secid + ".csv")
    return bars(p) if os.path.exists(p) else ([], {})

def index(name):
    return bars(os.path.join(ROOT, "data/cache/iss/index", name + ".csv"))

def week(d):
    y, w, _ = dt.date.fromisoformat(d).isocalendar()
    return (y, w)

def tstat(xs):
    if len(xs) < 3:
        return float("nan")
    s = st.stdev(xs)
    return st.mean(xs) / (s / math.sqrt(len(xs))) if s > 0 else float("nan")

def summary(events, key="ex"):
    """events: dict с полями day (YYYY-MM-DD) и key. → dict метрик."""
    xs = [e[key] for e in events if not math.isnan(e[key])]
    if not xs:
        return {"n": 0}
    wk = defaultdict(list)
    for e in events:
        if not math.isnan(e[key]):
            wk[week(e["day"])].append(e[key])
    wmeans = [st.mean(v) for v in wk.values()]
    return {"n": len(xs), "mean": st.mean(xs), "median": st.median(xs),
            "pos": sum(x > 0 for x in xs) / len(xs), "t": tstat(wmeans), "weeks": len(wmeans)}

def fmt(s):
    if s["n"] == 0:
        return "n=0"
    return (f"n={s['n']:4d} среднее {100*s['mean']:+6.2f}% медиана {100*s['median']:+6.2f}% "
            f"в плюсе {100*s['pos']:3.0f}% t={s['t']:+.2f} ({s['weeks']} нед.)")

def report(title, events, key="ex"):
    print(f"{title}: {fmt(summary(events, key))}")
    a = [e for e in events if e["day"] < "2024-01-01"]
    b = [e for e in events if e["day"] >= "2024-01-01"]
    print(f"   до 2024: {fmt(summary(a, key))}")
    print(f"   с 2024:  {fmt(summary(b, key))}")

def criterion(events, key="ex"):
    s = summary(events, key)
    a = summary([e for e in events if e["day"] < "2024-01-01"], key)
    b = summary([e for e in events if e["day"] >= "2024-01-01"], key)
    ok = s["n"] > 0 and s["mean"] > 0 and s["median"] > 0 and s["t"] > 2 and \
        a.get("mean", -1) > 0 and b.get("mean", -1) > 0
    return ok
