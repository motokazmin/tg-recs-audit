"""0036: пробой верхней полосы Боллинджера (20, 2σ) → лонг open(t+1) → close(t+5).
Превышение над LQDT (основное) и над MCFTR (контроль). Бумаги — обыкновенные акции вне 25 из 0018 бота.
LQDT до 2022-07 докачивается из ISS (TQTF) в data/cache/iss/shares-early/LQDT-TQTF.json."""
import bisect, csv, datetime as dt, json, math, os, statistics as st, time, urllib.request
from collections import Counter
from eventstats import ROOT, COST, index, report, summary, fmt, criterion

BOT25 = set("AFKS AFLT ALRS CHMF ENPG FEES FLOT GAZP IRAO LKOH MAGN MDMG MGNT MOEX "
            "MSNG MTSS NLMK NVTK PHOR PIKK POSI ROSN SBER TATN UPRO".split())
N, K, H = 20, 2.0, 5
MIN_TURNOVER = 50e6
JUMP = 0.40
EARLY = os.path.join(ROOT, "data/cache/iss/shares-early/LQDT-TQTF.json")


def get(url):
    for i in range(6):
        try:
            with urllib.request.urlopen(url, timeout=60) as r:
                return json.load(r)
        except Exception:
            time.sleep(2 * (i + 1))
    raise RuntimeError(url)


def lqdt():
    """Ряд LQDT {дата: close}: ранний кусок TQTF из ISS + кэш shares."""
    if not os.path.exists(EARLY):
        rows, start = [], 0
        while True:
            d = get("https://iss.moex.com/iss/history/engines/stock/markets/shares/boards/TQTF/securities/LQDT.json"
                    f"?from=2019-12-01&till=2022-07-31&start={start}&iss.meta=off&history.columns=TRADEDATE,CLOSE")["history"]
            rows += d["data"]
            if len(d["data"]) < 100:
                break
            start += 100
        tmp = EARLY + ".tmp"
        json.dump(rows, open(tmp, "w"))
        os.replace(tmp, EARLY)
    out = {d: c for d, c in json.load(open(EARLY)) if c}
    for r in csv.DictReader(open(os.path.join(ROOT, "data/cache/iss/shares/LQDT.csv"))):
        if float(r["close"] or 0) > 0:
            out[r["date"]] = float(r["close"])
    return out


def series(m):
    ds = sorted(m)
    return ds, [m[d] for d in ds]


def growth(ser, a, b):
    """Рост ряда между датами a и b: последнее значение не позже даты."""
    ds, vs = ser
    i, j = bisect.bisect_right(ds, a) - 1, bisect.bisect_right(ds, b) - 1
    return vs[j] / vs[i] - 1 if i >= 0 and j >= 0 else float("nan")


def load(secid):
    p = os.path.join(ROOT, "data/cache/iss/shares", secid + ".csv")
    if not os.path.exists(p):
        return None
    rows = {}
    for r in csv.DictReader(open(p)):
        c = float(r["close"] or 0)
        if r["board"] == "TQBR" and c > 0 and dt.date.fromisoformat(r["date"]).weekday() < 5:
            rows[r["date"]] = (float(r["open"] or 0) or c, c, float(r["volume"] or 0))
    return sorted(rows.items())


def events(secid, rows, mm, mc, skip):
    out = []
    d = [x[0] for x in rows]
    o = [x[1][0] for x in rows]
    c = [x[1][1] for x in rows]
    v = [x[1][2] for x in rows]
    cond = [False] * len(c)
    for i in range(N - 1, len(c)):
        w = c[i - N + 1:i + 1]
        cond[i] = c[i] > st.mean(w) + K * st.pstdev(w)
    for i in range(N, len(c) - H):
        if not cond[i] or cond[i - 1]:
            continue
        if any(abs(c[k] / c[k - 1] - 1) > JUMP for k in range(i - N + 1, i + H + 1)):
            skip["корп. действие (скачок >40%)"] += 1
            continue
        turnover = st.mean(c[k] * v[k] for k in range(i - N, i))
        if turnover < MIN_TURNOVER:
            skip["оборот < 50 млн"] += 1
            continue
        r = c[i + H] / o[i + 1] - 1 - 2 * COST
        bl, bm = growth(mm, d[i], d[i + H]), growth(mc, d[i], d[i + H])
        if math.isnan(bl) or math.isnan(bm):
            skip["нет бенчмарка"] += 1
            continue
        out.append({"day": d[i], "secid": secid, "r": r, "ex": r - bl, "exm": r - bm, "turnover": turnover})
    return out


def main():
    universe = [u["SecID"] for u in json.load(open(os.path.join(ROOT, "data/cache/iss/universe.json")))
                if u.get("Type") == "common_share"]
    mm = series(lqdt())
    ids, bars = index("MCFTR")
    mc = (ids, [bars[x][1] for x in ids])
    print(f"LQDT {mm[0][0]} → {mm[0][-1]}, MCFTR {ids[0]} → {ids[-1]}")

    new, old, skip = [], [], Counter()
    for secid in sorted(set(universe)):
        rows = load(secid)
        if not rows or len(rows) < N + H + 1:
            continue
        (old if secid in BOT25 else new).extend(events(secid, rows, mm, mc, skip))
    print(f"бумаг с событиями: новых {len({e['secid'] for e in new})}, из 0018 {len({e['secid'] for e in old})}")
    print("пропуски (обе выборки):", dict(skip))
    print(f"издержки {COST*1e4:.0f} б.п. на ногу, удержание {H} дней\n")

    print("=== ОСНОВНАЯ ВЫБОРКА: бумаги вне 0018 ===")
    report("сырой r (до бенчмарка)", new, "r")
    report("ex_lqdt (основная)", new, "ex")
    report("ex_mcftr (контроль)", new, "exm")
    main_ok = criterion(new, "ex")
    s = summary(new, "exm")
    ctrl_ok = s["n"] > 0 and s["mean"] > 0 and s["t"] > 2
    print(f"\nкритерий ex_lqdt: {'ПРОХОДИТ' if main_ok else 'нет'}; контроль ex_mcftr: {'ПРОХОДИТ' if ctrl_ok else 'нет'}")

    print("\n--- по годам (ex_lqdt | ex_mcftr) ---")
    for y in sorted({e["day"][:4] for e in new}):
        g = [e for e in new if e["day"].startswith(y)]
        print(f"{y}: {fmt(summary(g, 'ex'))} | ex_mcftr ср. {100*summary(g, 'exm')['mean']:+.2f}%")
    print("\n--- терцили оборота (ex_lqdt) ---")
    tv = sorted(e["turnover"] for e in new)
    cuts = [tv[len(tv) // 3], tv[2 * len(tv) // 3]]
    for name, lo, hi in (("нижняя", 0, cuts[0]), ("средняя", cuts[0], cuts[1]), ("верхняя", cuts[1], float("inf"))):
        g = [e for e in new if lo <= e["turnover"] < hi]
        print(f"{name:8} ({lo/1e6:6.0f}…{hi/1e6:6.0f} млн): {fmt(summary(g, 'ex'))}")

    print("\n=== ОПИСАТЕЛЬНО: 25 бумаг 0018, 2020+ (не независимо) ===")
    report("ex_lqdt", old, "ex")
    report("ex_mcftr", old, "exm")


if __name__ == "__main__":
    main()
