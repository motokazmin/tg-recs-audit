#!/usr/bin/env python3
"""Чувствительность премии размещений (0007) к аллокации — разбор 0012.

Прокси ажиотажа — close первого дня торгов (известен только после размещения, для решения
не годится; годится, чтобы оценить, сколько бэктест со 100% аллокацией переоценивает).
Читает data/analysis/placements.csv и окна истории из data/cache/iss/bondhead/.
"""
import csv, glob, os, statistics as st, sys

ROOT = os.path.join(os.path.dirname(__file__), "..")
SCEN = {  # аллокация по корзине close первого дня: (≤100.5, 100.5–102, >102)
    "0. как в бэктесте": (1.0, 1.0, 1.0),
    "1. умеренный": (1.0, 0.6, 0.3),
    "2. пессимистичный": (1.0, 0.4, 0.15),
    "3. только скучные": (1.0, 0.0, 0.0),
}

def day1_close(secid):
    files = glob.glob(os.path.join(ROOT, "data/cache/iss/bondhead", secid + "_*.csv"))
    if not files:
        return None
    rows = list(csv.DictReader(open(files[0])))
    for r in rows:
        if float(r["close"]) > 0:
            return float(r["close"])
    return None

def bucket(c):
    return 0 if c <= 100.5 else (1 if c <= 102 else 2)

rows = [r for r in csv.DictReader(open(os.path.join(ROOT, "data/analysis/placements.csv")))
        if r["n"] == "20" and r["no_trades"] == "false" and float(r["not_par"]) == 0]
obs = []
for r in rows:
    c = day1_close(r["secid"])
    if c is None:
        continue
    obs.append((bucket(c), float(r["excess"]), r["issue"]))
print(f"размещений: {len(obs)} (без close первого дня: {len(rows) - len(obs)})\n")
names = ["≤ 100.5% («скучные»)", "100.5–102%", "> 102% (ажиотаж)"]
print(f"{'close 1-го дня':24s} {'N':>5s} {'превыш. ср.':>11s} {'медиана':>8s} {'в плюсе':>8s}")
for b in range(3):
    xs = [e for k, e, _ in obs if k == b]
    if xs:
        print(f"{names[b]:24s} {len(xs):5d} {100*st.mean(xs):+10.2f}% {100*st.median(xs):+7.2f}% {100*sum(x>0 for x in xs)/len(xs):7.0f}%")
print(f"\n{'сценарий аллокации':22s} {'на вложенный ₽':>15s} {'на ₽ заявки':>12s} {'доля выдано':>12s}")
for name, a in SCEN.items():
    w = [a[k] for k, _, _ in obs]
    if sum(w) == 0:
        continue
    per_inv = sum(a[k] * e for k, e, _ in obs) / sum(w)
    per_req = sum(a[k] * e for k, e, _ in obs) / len(obs)
    print(f"{name:22s} {100*per_inv:+14.2f}% {100*per_req:+11.2f}% {100*sum(w)/len(obs):11.0f}%")
for half, keep in (("до 2025", lambda d: d < "2025"), ("с 2025", lambda d: d >= "2025")):
    xs = [e for k, e, d in obs if k == 0 and keep(d)]
    print(f"\n«скучные», размещения {half}: N {len(xs)}, среднее {100*st.mean(xs):+.2f}%, медиана {100*st.median(xs):+.2f}%, в плюсе {100*sum(x>0 for x in xs)/len(xs):.0f}%", end="")
print()
