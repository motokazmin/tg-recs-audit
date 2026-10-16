# paperd: запуск и эксплуатация

Сервис проверки идей вперёд на виртуальных деньгах (ADR 0005, регистрация 0013).
Три стратегии: `dividends`, `placements`, `placements-falling`. Пересчёт раз в сутки в 02:00 МСК
и по кнопке в админке; журналы — `data/paper/<стратегия>.jsonl`, память — `*.memory.json`,
день старта — `data/paper/start` (пишется при первом запуске и больше не меняется).

## Хост бота

`apps@64.188.96.204`, каталог `~/tg-recs-audit` (клон репо), пользовательская служба
(`sudo` у `apps` нет; linger включён — служба переживает выход и перезагрузку).
Go — `/usr/local/go/bin`, в PATH только у login-шелла, отсюда `bash -lc`.

```bash
ssh apps@64.188.96.204
cd ~/tg-recs-audit && bash -lc "make paperd-build"
mkdir -p ~/.config/systemd/user && cp deploy/paperd.service ~/.config/systemd/user/   # один раз
loginctl enable-linger apps                                                         # один раз
systemctl --user daemon-reload && systemctl --user enable --now paperd
journalctl --user -u paperd -f
```

Админка: `http://64.188.96.204:8092/?access_token=$ADMIN_TOKEN` (токен запоминается в cookie).
Порт задан флагом в юните: `HTTP_LISTEN` в `/etc/trading-bot/env` — порт бота 8091, и
`EnvironmentFile` перекрывает `Environment=`. Без `ADMIN_TOKEN` сервис не стартует на 0.0.0.0.

Первый запуск качает кэши ISS (список облигаций, карточки) — ~7 минут; HTTP поднимается
после загрузки списка акций (~20 с).

## Локально

```bash
make paperd          # фон, 127.0.0.1:8092, PID → data/paperd.pid, лог → data/paperd.log
make paperd-status
make paperd-stop
```

## Обновление кода

`git pull && bash -lc "make paperd-build" && systemctl --user restart paperd`. Если правка меняет расчёт
прошлого — журнал стратегии остановится («ОСТАНОВЛЕНА» в админке, тревога в Telegram): это не
сбой, а решение человека — новая стратегия с новым именем (журнал старой сохраняется), запись в 0013.

## Тревоги

Ошибка пересчёта или расхождение журнала → Telegram. Остановленная стратегия не пересчитывается
до перезапуска сервиса.
