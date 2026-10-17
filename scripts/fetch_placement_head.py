"""Первые торговые дни размещений 0007 с официальным закрытием и средневзвешенной (0019).
Кэш data/cache/iss/plhead/<secid>.json."""
import csv, json, os, time, urllib.request
from concurrent.futures import ThreadPoolExecutor

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CACHE = os.path.join(ROOT, "data/cache/iss/plhead")
COLS = "TRADEDATE,BOARDID,NUMTRADES,VALUE,OPEN,CLOSE,LEGALCLOSEPRICE,WAPRICE,ACCINT,FACEVALUE"

def get(url):
    for i in range(5):
        try:
            with urllib.request.urlopen(url, timeout=60) as r:
                return json.load(r)
        except Exception:
            time.sleep(2 * (i + 1))
    raise RuntimeError(url)

def fetch(row):
    secid, issue = row
    path = os.path.join(CACHE, secid + ".json")
    if os.path.exists(path):
        return
    y, m, d = map(int, issue.split("-"))
    import datetime as dt
    till = (dt.date(y, m, d) + dt.timedelta(20)).isoformat()
    h = get(f"https://iss.moex.com/iss/history/engines/stock/markets/bonds/securities/{secid}.json"
            f"?from={issue}&till={till}&iss.meta=off&history.columns={COLS}")["history"]
    json.dump({"columns": COLS.split(","), "history": h["data"]}, open(path, "w"))

if __name__ == "__main__":
    os.makedirs(CACHE, exist_ok=True)
    rows = {(r["secid"], r["issue"]) for r in csv.DictReader(open(os.path.join(ROOT, "data/analysis/placements.csv")))}
    with ThreadPoolExecutor(6) as ex:
        list(ex.map(fetch, sorted(rows)))
    print("выпусков:", len(rows))
