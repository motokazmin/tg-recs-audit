# Архитектура: карта сущностей

Проверка рекомендаций телеграм-каналов виртуальным портфелем на истории MOEX.
Решения — в [`adr/`](adr/), разборы — в [`analysis/`](analysis/).

## Две зоны

| Зона | Каталог | Меняется |
|------|---------|----------|
| **Каркас: доступ к внешнему миру** | `internal/infra/**` | редко |
| **Прикладной код: идеи, портфель, проверки** | `internal/feature/**` | часто |
| **Контракт (leaf)** | `internal/contract` | при новом порте |
| **Каркас paper-сервиса: расписание, журналы, оценка, админка** | `internal/paper` | редко |
| **Общая сверка журналов** | `internal/ledger` (`Reconcile`) | редко |
| **Composition root** | `internal/app` | при новой команде или источнике |

`cmd/recaudit/main.go` — оглавление: `app.ParseFlags` → `app.Run`.
`cmd/paperd/main.go` — оглавление: `app.ParsePaperFlags` → `app.RunPaper`.

## Порты (`internal/contract`)

| Порт | Реализации | Кто потребляет |
|------|------------|----------------|
| `ChannelSource` — история канала | `infra/tgweb` (веб-превью `t.me/s/`, любой публичный канал), `infra/tgexport` (`result.json` или каталог HTML-экспорта Telegram Desktop) | `app` → `feature/*` |
| `PriceHistory` — дневные бары бумаги | `infra/moexiss` (ISS, кэш `data/cache/iss`) | `feature/portfolio`, `feature/historycheck` |
| `DividendSource` — выплаты, суммы в масштабе текущей акции | `infra/smartlab` (кэш `data/cache/smartlab`, неделя; в paperd — сутки) | `feature/portfolio` (приводит масштаб по сплитам, сверяет доходность) |
| `SecurityUniverse` — все бумаги, включая делистнутые | `infra/moexiss` | `feature/mentions`, `app` (известные тикеры для `ideas`) |
| `Ledger` — журнал операций, только дописывается | `infra/ledgerfile` (JSONL в `data/ledger`) | `app` |
| `InstrumentResolver` — «ОФЗ 26244», ISIN, тикер → бумага | `infra/moexiss` (поиск + карточка, кэш навсегда) | `feature/diary` |
| `BondData` — бары облигаций (% номинала, НКД) и выплаты | `infra/moexiss` (history + bondization) | `feature/diary` |
| `FXRates` — курс ЦБ для номинала не в рублях | `infra/cbr` | `feature/diary` |

Закрытый канал без веб-превью — новая реализация `ChannelSource` (MTProto); выбирается в
`app.ChannelSource` по виду ссылки. Прикладной код об источнике не знает.

## Сборка команды `run`

```
app.runPortfolio
  ChannelSource(ref).Messages()         посты: кэш data/channels/<канал>.json + докачка новых
  moexiss.Shares()                      известные тикеры
  ideas.Parse(msgs, known)              идеи + отказы с причиной
  loadCorpActions()                     data/corporate_actions.json — сплиты, смены тикеров, заморозки
  moexiss.Prefetch(тикеры)              дневные бары, 4 потока, дозагрузка хвоста
  smartlab.New                          дивиденды (нет при -no-dividends)
  moexiss.IndexBars("MCFTR"|"IMOEX")    бенчмарк: полной доходности с дивидендами, ценовой без
  portfolio.Run(...)                    прогон: позиции, пропуски, ожидающие, ПРОБЛЕМЫ
    └ проблемы есть → стоп, журнал не трогаем
  portfolio.Ops → ledger.Reconcile      сверка с журналом; прошлое изменилось → стоп
  ledgerfile.Append                     дописать новые операции
```

## Сервис `paperd` (ADR 0005, регистрация 0013)

Проверка идей вперёд на виртуальных деньгах. Live = бэктест «на вчера»: стратегия — функция
`Ops(asOf, mem)` всех операций со старта, каркас её пересчитывает и дописывает разницу.

| Сущность | Где | Роль |
|----------|-----|------|
| `paper.Strategy` | `internal/paper/paper.go` | порт стратегии: `Ops`, `Mark` (оценка), счёт, старт, бенчмарк |
| `paper.Memory` | там же | что стратегия уже видела (пост, подтверждение выплаты): удалённый пост не меняет прошлое |
| `paper.Runner` | `runner.go` | пересчёт всех стратегий (ночью и по кнопке, не одновременно) → `ledger.Reconcile` → `Append`; прошлое изменилось → «ОСТАНОВЛЕНА» + тревога |
| `paper.Curve`, `Trades`, `Summarize` | `account.go` | капитал по дням бенчмарка, сделки и превышение из журнала |
| `paper.Server` | `server.go` | админка: сводка, страница стратегии (SVG, позиции, сделки, журнал), `/api/status`, `POST /run`; токен Bearer / `?access_token=` → cookie |
| `paperstrat.Dividends`, `Placements` | `internal/feature/paperstrat` | стратегии A, B, C из 0013 |
| `telegram.Alerter` | `internal/infra/telegram` | тревоги; без `TG_*` — только лог |

Сборка (`app.RunPaper`): `paper.ValidateListen` → день старта из `data/paper/start` (пишется
один раз) → `moexiss` (списки бумаг и облигаций — кэш сутки), `smartlab` (сутки) → стратегии →
`paper.NewRunner` → `paper.NewServer` → `Runner.Schedule(02:00 МСК)` + HTTP. Эксплуатация —
[`paperd.md`](paperd.md).

## Команда `houses`

Тот же `prepare`, что у `run`, затем `houses.Compare`: идеи группируются по дому
(`data/analyst_aliases.json`), каждая группа прогоняется **отдельным** `portfolio.Run`
(функция прогона приходит из `app`, feature не знает об источниках), период делится
по `-split`, считается t превышения над IMOEX и ранговая корреляция между периодами.
Журнал не пишется.

## Команда `diary`

Портфель-дневник (ADR 0004): `diary.Parse` (взносы, сделки, «общие позиции», снимки
#БалансПортфеля; синонимы и правки — `data/diary/<канал>/`) → `diary.Run` дважды (автор,
последователь): согласование количества и цены с рынком один раз → день за днём долг, сплиты
и смены тикеров, выплаты облигаций, взносы, сделки, дивиденды, оценка; бенчмарки MCFTR и LQDT →
журнал последователя через `ledger.Reconcile`.

## Команда `placements`

Проверка 0006/0007: `moexiss.Bonds` (все облигации, с погашенными) → карточки параллельно
(`Card`, кэш навсегда) → `placements.Universe` → по выпуску `BondHead` (окно после размещения)
и `BondFlows` → `placements.Evaluate` против LQDT → сводка с t по неделям размещения.
`placements.Market` — порт на стороне потребителя. `moexiss.Bonds` — кэш сутки (paperd ждёт новые выпуски).

## Команды `signals` и `divevents`

`signals` (0008/0009): `signals.Parse` — карточки сигналов по фьючерсам (инструмент-контракт →
код FORTS: префикс + буква месяца + цифра года) → `signals.Simulate` на 10-минутках ISS
(`moexiss.FuturesBars`, кэш по дню) — исполнение как в боте; диагностика «вход по рынку».
`divevents` (0010/0011): `divevents.Parse` — посты «Рекомендованы дивиденды» → `Evaluate` на
дневных барах против MCFTR; выплата подтверждается smart-lab.

## Граф зависимостей

```
cmd/recaudit → app → feature/{ideas,portfolio,houses,diary,placements,signals,divevents,channelcheck,historycheck,mentions} → contract
                  └→ infra/{tgweb,tgexport,moexiss,smartlab,cbr,ledgerfile}            → contract
feature/portfolio → feature/ideas
feature/houses → feature/{portfolio,ideas}
feature/channelcheck → feature/mentions

cmd/paperd → app → feature/paperstrat → paper → {contract, ledger, infra/ledgerfile}
                 │                     └→ feature/{divevents,placements}
                 └→ infra/{moexiss,smartlab,tgweb,telegram}
app (run, diary) → ledger
```
infra и feature друг друга не импортируют.

## Данные

| Путь | Что | В git |
|------|-----|-------|
| `data/channels/<канал>.json` | снимок постов | да — оригинал на случай удаления постов каналом |
| `data/corporate_actions.json` | ручная таблица: сплиты, смены тикеров, заморозки, правки дивидендов | да |
| `data/analyst_aliases.json` | синонимы аналитических домов | да |
| `data/diary/<канал>/{aliases,fixes}.json` | синонимы бумаг и правки разбора дневника | да |
| `data/exports/` | экспорты Telegram закрытых каналов | **нет** — платный контент |
| `data/ledger/<канал>-<горизонт>[-stack][-price].jsonl` | журнал операций; `-price` — без дивидендов | да |
| `data/cache/iss/` | кэш ISS | нет |
| `data/paper/<стратегия>.jsonl`, `*.memory.json`, `start` | журналы paperd, память стратегий, день старта | нет — живут на хосте |
| `data/analysis/positions-*.csv` | позиции последнего прогона | нет |
