"""0014, идея 5: акции, впервые торговавшиеся на TQBR 2023-01…2026-06. Вход close 1-го дня,
выход close через 60 торговых дней, против MCFTR."""
import bisect, csv, json, os, sys
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, share, index, summary, fmt, report

HOLD = 60
# Не IPO: бумага торговалась раньше под другим кодом или за рубежом (по памяти — сверить при сомнении).
EXCLUDE = {
    "ABIO": "смена кода ISKJ → ABIO", "CNRU": "редомициляция Циан (CIAN)", "ELFV": "переименование Энел Россия (ENRU)",
    "EVRZ": "Евраз торговался в Лондоне", "FIXR": "редомициляция Fix Price (FIXP)", "HEAD": "редомициляция HeadHunter (HHRU)",
    "KFBA": "Инград (INGR), смена кода", "RAGR": "редомициляция Русагро (AGRO)", "SOFL": "Софтлайн — ГДР в Лондоне, прямой листинг",
    "T": "редомициляция ТКС (TCSG)", "X5": "редомициляция X5 (FIVE)", "YDEX": "редомициляция Яндекса (YNDX)",
}
md, mc = index("MCFTR")
u = json.load(open(os.path.join(ROOT, "data/cache/iss/universe.json")))
events = []
for x in u:
    if x["Type"] != "common_share" or x["SecID"] in EXCLUDE:
        continue
    days, b = share(x["SecID"])
    if not days or not ("2023-01-01" <= days[0] <= "2026-06-30"):
        continue
    d0 = days[0]
    first_day = b[d0][1] / b[d0][0] - 1
    if len(days) <= HOLD:
        print(f"  {x['SecID']}: меньше {HOLD} дней торгов — без выхода")
        continue
    d1 = days[HOLD]
    ret = b[d1][1] * (1 - COST) / (b[d0][1] * (1 + COST)) - 1
    i, j = bisect.bisect_right(md, d0) - 1, bisect.bisect_right(md, d1) - 1
    ex = ret - (mc[md[j]][1] / mc[md[i]][1] - 1)
    events.append({"day": d0, "secid": x["SecID"], "ex": ex, "first": first_day, "ret": ret})
print(f"== Новые листинги, вход close 1-го дня, выход через {HOLD} торг. дней, против MCFTR")
print("Исключены:", "; ".join(f"{k} — {v}" for k, v in sorted(EXCLUDE.items())))
report("превышение", events)
s = summary(events)
neg = 1 - s["pos"]
print(f"доля в минусе к MCFTR: {100*neg:.0f}%")
print(f"первый день open→close (описательно): {fmt(summary(events, 'first'))}")
for e in sorted(events, key=lambda e: e["day"]):
    print(f"   {e['day']} {e['secid']:6s} первый день {100*e['first']:+6.1f}%  за {HOLD} дн. {100*e['ret']:+6.1f}%  к MCFTR {100*e['ex']:+6.1f}%")
ok = s["mean"] < 0 and s["median"] < 0 and neg >= 0.6
print("КРИТЕРИЙ (недоходность):", "выполнен" if ok else "НЕ выполнен")
