"""Состав IMOEX до и после квартальной ребалансировки (третья пятница мар/июн/сен/дек), 2020–2026.
Кэш data/cache/iss/imoex-rebalances.json. Проверка 0014, идея 6."""
import datetime as dt, json, os, time, urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PATH = os.path.join(ROOT, "data/cache/iss/imoex-rebalances.json")

def comp(day):
    for _ in range(5):
        try:
            u = f"https://iss.moex.com/iss/statistics/engines/stock/markets/index/analytics/IMOEX.json?date={day}&limit=100&iss.meta=off"
            d = json.load(urllib.request.urlopen(u, timeout=60))["analytics"]
            return d["data"][0][1] if d["data"] else None, sorted({r[2] for r in d["data"]})
        except Exception:
            time.sleep(3)
    raise RuntimeError(day)

def third_friday(y, m):
    d = dt.date(y, m, 15)
    return d + dt.timedelta((4 - d.weekday()) % 7)

out = []
for y in range(2020, 2027):
    for m in (3, 6, 9, 12):
        f = third_friday(y, m)
        if f > dt.date(2026, 9, 30):
            continue
        before_day, before = comp((f - dt.timedelta(7)).isoformat())  # за неделю: на пятницу ISS уже даёт новый состав
        after_day, after = None, []
        for k in range(3, 8):                              # понедельник и дальше
            after_day, after = comp((f + dt.timedelta(k)).isoformat())
            if after and after_day > f.isoformat():
                break
        out.append({"friday": f.isoformat(), "before_day": before_day, "after_day": after_day,
                    "added": sorted(set(after) - set(before)), "removed": sorted(set(before) - set(after))})
        print(out[-1])
json.dump(out, open(PATH, "w"), ensure_ascii=False)
