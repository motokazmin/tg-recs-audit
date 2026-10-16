# tg-recs-audit

Проверяем телеграм-каналы с рекомендациями: повторяем их на истории виртуальными деньгами,
находим рабочие механизмы и проверяем их на всём рынке. Итог — `docs/ideas.md`.

```
make check-channel CHANNEL=t.me/имя        # годится ли история канала
make run CHANNEL=t.me/ruforecasts           # идеи с целью: портфель, журнал
make houses                                 # идеи по аналитическим домам
make diary                                  # портфель-дневник (Trendoman, экспорт в data/exports/)
make placements                             # размещения облигаций против LQDT
```

Новый канал — по шагам `docs/channel-review.md`. Код — `docs/architecture.md`.
