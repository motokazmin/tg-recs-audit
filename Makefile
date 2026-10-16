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

# Пригодна ли история канала: глубина, удалённые, правки, тикеры
check-channel: $(BIN)
	$(BIN) check-channel $(CHANNEL)

test:
	$(GO) test ./...

.PHONY: run houses diary placements check-channel test
