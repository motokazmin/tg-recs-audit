GO := GOTOOLCHAIN=local go
CHANNEL ?= t.me/ruforecasts
BIN := bin/recaudit

$(BIN): $(shell find cmd internal -name '*.go') go.mod
	$(GO) build -o $(BIN) ./cmd/recaudit

# Докачать посты и цены, прогнать портфель, дописать журнал. ARGS="-horizon 12m", "-stack", "-dry"
run: $(BIN)
	$(BIN) run $(ARGS) $(CHANNEL)

# Идеи по аналитическим домам: устойчив ли рейтинг между периодами
houses: $(BIN)
	$(BIN) houses $(ARGS) $(CHANNEL)

# Портфель-дневник (Trendoman): повторить сделки автора, дописать журнал последователя
diary: $(BIN)
	$(BIN) diary $(ARGS) data/exports/trendoman

# Размещения облигаций по номиналу против LQDT (0006/0007)
placements: $(BIN)
	$(BIN) placements $(ARGS)

# Сигналы по фьючерсам карточкой (0008/0009): make signals CHANNEL=t.me/Exper_Trading
signals: $(BIN)
	$(BIN) signals $(ARGS) $(CHANNEL)

# Рекомендации дивидендов как событие (0010/0011): make divevents CHANNEL=t.me/divonline
divevents: $(BIN)
	$(BIN) divevents $(ARGS) $(CHANNEL)

# Пригодна ли история канала: глубина, удалённые, правки, тикеры
check-channel: $(BIN)
	$(BIN) check-channel $(CHANNEL)

# paperd — проверка идей вперёд (docs/paperd.md)
paperd-build:
	$(GO) build -o bin/paperd ./cmd/paperd
	$(GO) build -o $(BIN) ./cmd/recaudit

paperd: paperd-build
	@if [ -f data/paperd.pid ] && kill -0 $$(cat data/paperd.pid) 2>/dev/null; then echo "paperd уже работает (PID $$(cat data/paperd.pid))"; exit 1; fi
	nohup bin/paperd > data/paperd.log 2>&1 & echo $$! > data/paperd.pid
	@echo "paperd запущен (PID $$(cat data/paperd.pid)), лог data/paperd.log"

paperd-stop:
	@if [ -f data/paperd.pid ]; then kill $$(cat data/paperd.pid) && rm -f data/paperd.pid && echo остановлен; else echo "не запущен"; fi

paperd-status:
	@if [ -f data/paperd.pid ] && kill -0 $$(cat data/paperd.pid) 2>/dev/null; then echo "работает (PID $$(cat data/paperd.pid))"; tail -5 data/paperd.log; else echo "не запущен"; fi

test:
	$(GO) test ./...

.PHONY: run houses diary placements signals divevents check-channel test paperd paperd-build paperd-stop paperd-status
