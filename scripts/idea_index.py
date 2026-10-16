"""0014, идея 6: включение в IMOEX. Разгон — close за 10 торговых дней до вступления → close
последнего дня старого состава; откат — +20 дней после. Против MCFTR."""
import bisect, json, os, sys
sys.path.insert(0, os.path.dirname(__file__))
from eventstats import ROOT, COST, share, index, summary, fmt, report, criterion

LEAD, AFTER = 10, 20
# Замена кода у той же компании, а не включение (редомициляции).
SWAP = {("2024-09-20", "YDEX"): "Яндекс YNDX→YDEX", ("2024-12-20", "HEAD"): "HeadHunter HHRU→HEAD",
        ("2025-09-19", "X5"): "X5 FIVE→X5", ("2025-03-21", "MDMG"): "Мать и дитя, редомициляция",
        ("2025-12-19", "CNRU"): "Циан CIAN→CNRU", ("2026-06-19", "RAGR"): "Русагро AGRO→RAGR",
        ("2025-12-19", "OZON"): "Озон после редомициляции"}
md, mc = index("MCFTR")
reb = json.load(open(os.path.join(ROOT, "data/cache/iss/imoex-rebalances.json")))

def window(secid, eff_last, lead, after):
    days, b = share(secid)
    k = bisect.bisect_right(days, eff_last) - 1
    if k < 0 or days[k] != eff_last:
        return None
    out = {}
    if k - lead >= 0:
        d0, d1 = days[k - lead], days[k]
        r = b[d1][1] * (1 - COST) / (b[d0][1] * (1 + COST)) - 1
        out["run"] = r - (mc[md[bisect.bisect_right(md, d1) - 1]][1] / mc[md[bisect.bisect_right(md, d0) - 1]][1] - 1)
    if k + after < len(days):
        d0, d1 = days[k], days[k + after]
        r = b[d1][1] * (1 - COST) / (b[d0][1] * (1 + COST)) - 1
        out["post"] = r - (mc[md[bisect.bisect_right(md, d1) - 1]][1] / mc[md[bisect.bisect_right(md, d0) - 1]][1] - 1)
    return out

adds, rems = [], []
for r in reb:
    if len(r["added"]) > 20:      # перезапуск индекса или пробел в данных, а не ребалансировка
        continue
    for s in r["added"]:
        if (r["friday"], s) in SWAP:
            print(f"  пропуск {r['friday']} {s}: {SWAP[r['friday'], s]}")
            continue
        w = window(s, r["friday"], LEAD, AFTER)
        if w and "run" in w:
            adds.append({"day": r["friday"], "secid": s, "ex": w["run"], "post": w.get("post", float("nan"))})
        else:
            print(f"  пропуск {r['friday']} {s}: нет {LEAD} дней торгов до вступления")
    for s in r["removed"]:
        w = window(s, r["friday"], LEAD, AFTER)
        if w and "run" in w:
            rems.append({"day": r["friday"], "secid": s, "ex": w["run"], "post": w.get("post", float("nan"))})
print(f"== Включение в IMOEX: разгон {LEAD} дней до вступления, против MCFTR")
report("включения, разгон", adds)
print(f"включения, +{AFTER} дней после: {fmt(summary(adds, 'post'))}")
print(f"исключения, {LEAD} дней до: {fmt(summary(rems))}")
print(f"исключения, +{AFTER} дней после: {fmt(summary(rems, 'post'))}")
for e in adds:
    print(f"   {e['day']} {e['secid']:6s} разгон {100*e['ex']:+6.1f}%  после {100*e['post']:+6.1f}%")
print("КРИТЕРИЙ:", "выполнен" if criterion(adds) else "НЕ выполнен")
