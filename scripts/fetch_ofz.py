"""ОФЗ: история ISS по всем доскам (TQOB — вторичка, PACT — аукцион Минфина) и купоны.
Кэш data/cache/iss/ofz/<secid>.json. Проверка 0014, идея 2."""
import json, os, sys, time, urllib.request
from concurrent.futures import ThreadPoolExecutor

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CACHE = os.path.join(ROOT, "data/cache/iss/ofz2")  # с ценами закрытия и объёмом (0016)
COLS = "TRADEDATE,BOARDID,OPEN,LOW,CLOSE,LEGALCLOSEPRICE,WAPRICE,ACCINT,FACEVALUE,NUMTRADES,VALUE,VOLUME"

def get(url):
    for i in range(5):
        try:
            with urllib.request.urlopen(url, timeout=60) as r:
                return json.load(r)
        except Exception:
            time.sleep(2 * (i + 1))
    raise RuntimeError(url)

def fetch(secid):
    path = os.path.join(CACHE, secid + ".json")
    if os.path.exists(path):
        return
    rows, start = [], 0
    while True:
        d = get(f"https://iss.moex.com/iss/history/engines/stock/markets/bonds/securities/{secid}.json"
                f"?from=2020-01-01&iss.meta=off&start={start}&history.columns={COLS}")["history"]
        rows += d["data"]
        if len(d["data"]) < 100:
            break
        start += len(d["data"])
    b = get(f"https://iss.moex.com/iss/securities/{secid}/bondization.json?iss.meta=off&limit=unlimited")
    c = b["coupons"]
    coupons = [dict(zip(c["columns"], r)) for r in c["data"]]
    json.dump({"columns": COLS.split(","), "history": rows,
               "coupons": [{"date": x["coupondate"], "value": x["value_rub"] or x["value"]} for x in coupons]},
              open(path, "w"))

if __name__ == "__main__":
    os.makedirs(CACHE, exist_ok=True)
    u = json.load(open(os.path.join(ROOT, "data/cache/iss/bonds-universe.json")))
    ids = sorted(x["SecID"] for x in u if x["SecID"].startswith("SU") and "RMFS" in x["SecID"])
    with ThreadPoolExecutor(4) as ex:
        list(ex.map(fetch, ids))
    print("ОФЗ:", len(ids))
