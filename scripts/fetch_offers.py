"""Оферты по облигациям (ISS statistics bondization) 2021…2026-09 и история выпусков с офертой.
Кэш data/cache/iss/offers/. Проверка 0014, идея 4."""
import json, os, time, urllib.request
from concurrent.futures import ThreadPoolExecutor

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CACHE = os.path.join(ROOT, "data/cache/iss/offers")
COLS = "TRADEDATE,BOARDID,CLOSE,ACCINT,FACEVALUE,NUMTRADES"

def get(url):
    for i in range(5):
        try:
            with urllib.request.urlopen(url, timeout=60) as r:
                return json.load(r)
        except Exception:
            time.sleep(2 * (i + 1))
    raise RuntimeError(url)

def offers():
    path = os.path.join(CACHE, "offers.json")
    if os.path.exists(path):
        return json.load(open(path))
    out = []
    for y in range(2021, 2027):
        start = 0
        while True:
            d = get("https://iss.moex.com/iss/statistics/engines/stock/markets/bonds/bondization.json"
                    f"?iss.only=offers&from={y}-01-01&till={y}-12-31&limit=100&start={start}&iss.meta=off")["offers"]
            out += [dict(zip(d["columns"], r)) for r in d["data"]]
            if len(d["data"]) < 100:
                break
            start += 100
    json.dump(out, open(path, "w"))
    return out

def history(secid):
    path = os.path.join(CACHE, secid + ".json")
    if os.path.exists(path):
        return
    rows, start = [], 0
    while True:
        d = get(f"https://iss.moex.com/iss/history/engines/stock/markets/bonds/securities/{secid}.json"
                f"?from=2020-06-01&iss.meta=off&start={start}&history.columns={COLS}")["history"]
        rows += d["data"]
        if len(d["data"]) < 100:
            break
        start += len(d["data"])
    b = get(f"https://iss.moex.com/iss/securities/{secid}/bondization.json?iss.meta=off&limit=unlimited")
    c, a = b["coupons"], b["amortizations"]
    flows = [{"date": r[c["columns"].index("coupondate")], "value": r[c["columns"].index("value_rub")] or 0, "amort": False} for r in c["data"]]
    flows += [{"date": r[a["columns"].index("amortdate")], "value": r[a["columns"].index("value_rub")] or 0, "amort": True} for r in a["data"]]
    json.dump({"columns": COLS.split(","), "history": rows, "flows": flows}, open(path, "w"))

if __name__ == "__main__":
    os.makedirs(CACHE, exist_ok=True)
    of = offers()
    import collections
    print("оферт:", len(of), collections.Counter(o["offertype"] for o in of).most_common())
    ids = sorted({o["secid"] for o in of if o["faceunit"] in ("RUB", "SUR") and "Погашение" not in (o["offertype"] or "")
                  and "2021-01-01" <= (o["offerdate"] or "") <= "2026-09-30"})
    print("выпусков к загрузке:", len(ids))
    with ThreadPoolExecutor(6) as ex:
        list(ex.map(history, ids))
    print("готово")
