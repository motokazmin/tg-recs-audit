"""Отбор размещений 0006 (`placements.Universe`) в Python — для разборов 0033/0034 — и общие загрузки:
карточки ISS (кэш `cards/`, как у Go), ключевая ставка ЦБ.
matured: "" — как 0006; "with" — вернуть погашенные обычные выпуски, у которых ISS убрал купон
из карточки; "only" — только они (0034)."""
import json, os, re, time, urllib.request
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from eventstats import ROOT

NOTE = re.compile(r"(?i)структур|ПБО-\d+Р-К|ВТБ С-|ВТБ \(ПАО\) С-|RIB|CIB")
CARDS = os.path.join(ROOT, "data/cache/iss/cards")
KR = os.path.join(ROOT, "data/cache/cbr/keyrate-2020-2022.csv")

def get(url):
    for i in range(6):
        try:
            with urllib.request.urlopen(url, timeout=60) as r:
                return r.read()
        except Exception:
            time.sleep(2 * (i + 1))
    raise RuntimeError(url)

def card(secid):
    path = os.path.join(CARDS, secid + ".json")
    if not os.path.exists(path):
        d = json.loads(get(f"https://iss.moex.com/iss/securities/{secid}.json?iss.only=description&iss.meta=off"))["description"]
        m = {r[0]: str(r[2]) if r[2] is not None else "" for r in d["data"]}
        tmp = path + ".tmp"
        json.dump(m, open(tmp, "w"))
        os.replace(tmp, path)
    return json.load(open(path))

def keyrate():
    if not os.path.exists(KR):
        s = get("https://www.cbr.ru/hd_base/KeyRate/?UniDbQuery.Posted=True&UniDbQuery.From=01.12.2020&UniDbQuery.To=31.12.2022").decode()
        rows = re.findall(r"<td>(\d\d)\.(\d\d)\.(\d{4})</td>\s*<td>([\d,]+)</td>", s)
        with open(KR, "w") as f:
            f.write("date,rate\n")
            for d, m, y, r in sorted(rows, key=lambda x: (x[2], x[1], x[0])):
                f.write(f"{y}-{m}-{d},{r.replace(',', '.')}\n")
    out = [l.strip().split(",") for l in open(KR)][1:]
    return [d for d, _ in out], [float(r) / 100 for _, r in out]

def select(frm, to, matured=""):
    """→ [(secid, ISSUEDATE)], пропуски по причинам."""
    universe = json.load(open(os.path.join(ROOT, "data/cache/iss/bonds-universe.json")))
    ids = [s["SecID"] for s in universe if s["Type"] in ("corporate_bond", "exchange_bond")
           and s["SecID"].startswith("RU000A") and s["SecID"] >= "RU000A101" and s["SecID"] < "RU000B"]
    with ThreadPoolExecutor(6) as ex:
        cards = dict(zip(ids, ex.map(card, ids)))
    skip, sel = Counter(), []
    for sid, c in cards.items():
        issue = c.get("ISSUEDATE", "")
        if not (frm <= issue <= to):
            continue
        if c.get("FACEUNIT") not in ("SUR", "RUB"):
            skip["номинал не в рублях"] += 1; continue
        if "ИОС" in c.get("NAME", ""):
            skip["ИОС"] += 1; continue
        try:
            cp = float(c.get("COUPONPERCENT") or "nan")
        except ValueError:
            cp = float("nan")
        if cp < 1:
            skip["купон < 1%"] += 1; continue
        missed = cp != cp and not c.get("COUPONVALUE")
        if missed and not (matured and c.get("COUPONFREQUENCY") and not NOTE.search(c.get("NAME", ""))):
            skip["купона нет"] += 1; continue
        if matured == "only" and not missed:
            continue
        sel.append((sid, issue))
    return sel, skip
