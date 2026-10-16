# paperd: запуск и эксплуатация

Сервис проверки идей вперёд на виртуальных деньгах (ADR 0005, регистрация 0013).
Три стратегии: `dividends`, `placements`, `placements-falling`. Пересчёт раз в сутки в 02:00 МСК
и по кнопке в админке; журналы — `data/paper/<стратегия>.jsonl`, память — `*.memory.json`,
день старта — `data/paper/start` (пишется при первом запуске и больше не меняется).

## Хост бота

```bash
git clone git@github.com:motokazmin/tg-recs-audit.git ~/tg-recs-audit   # или git pull
cd ~/tg-recs-audit && make paperd-build
# ADMIN_TOKEN, TG_BOT_TOKEN, TG_CHAT_ID уже в /etc/trading-bot/env (общий с ботом)
sudo cp deploy/paperd.service /etc/systemd/system/      # проверить User и пути
sudo systemctl daemon-reload && sudo systemctl enable --now paperd
journalctl -u paperd -f
```

Админка: `http://ХОСТ:8092/?access_token=$ADMIN_TOKEN` (токен запоминается в cookie).
Firewall: TCP **8092**. Без `ADMIN_TOKEN` сервис не стартует на 0.0.0.0.

Первый запуск качает кэши ISS (список облигаций, карточки ~6 тыс., бары) — 10–15 минут.

## Локально

```bash
make paperd          # фон, 127.0.0.1:8092, PID → data/paperd.pid, лог → data/paperd.log
make paperd-status
make paperd-stop
```

## Обновление кода

`git pull && make paperd-build && sudo systemctl restart paperd`. Если правка меняет расчёт
прошлого — журнал стратегии остановится («ОСТАНОВЛЕНА» в админке, тревога в Telegram): это не
сбой, а решение человека — новая стратегия с новым именем (журнал старой сохраняется), запись в 0013.

## Тревоги

Ошибка пересчёта или расхождение журнала → Telegram. Остановленная стратегия не пересчитывается
до перезапуска сервиса.
