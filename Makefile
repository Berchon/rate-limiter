APP_NAME := rate-limiter
BIN_DIR := bin
BIN_PATH := $(BIN_DIR)/$(APP_NAME)
MAIN_PKG := ./cmd/webapp

UNIT_COV_FILE := coverage-unit.out
INTEGRATION_COV_FILE := coverage-integration.out
TOTAL_COV_FILE := coverage.out
COV_HTML_FILE := coverage.html

# Progressive coverage thresholds (set to 85 in Phase 2 / T14)
UNIT_MIN_COV ?= 0
INTEGRATION_MIN_COV ?= 0

GOLANGCI_LINT ?= golangci-lint

.PHONY: build run test test-unit test-integration coverage lint setup-hooks docker-up docker-down clean

build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -o $(BIN_PATH) $(MAIN_PKG)

run:
	go run $(MAIN_PKG)

test:
	go test -race ./...

test-unit:
	go test -race -covermode=atomic -coverprofile=$(UNIT_COV_FILE) ./internal/... ./cmd/...
	@cov=$$(go tool cover -func=$(UNIT_COV_FILE) | awk '/^total:/ {gsub(/%/, "", $$3); print $$3}'); \
	echo "Unit coverage: $${cov:-0}% (minimum: $(UNIT_MIN_COV)%)"; \
	awk -v cov="$${cov:-0}" -v min="$(UNIT_MIN_COV)" 'BEGIN { if (cov + 0 < min + 0) { printf "FAIL: unit coverage %.2f%% is below minimum %.2f%%\n", cov, min; exit 1 } }'

test-integration:
	go test -race -covermode=atomic -coverpkg=./internal/... -coverprofile=$(INTEGRATION_COV_FILE) ./tests/integration/...
	@cov=$$(go tool cover -func=$(INTEGRATION_COV_FILE) | awk '/^total:/ {gsub(/%/, "", $$3); print $$3}'); \
	echo "Integration coverage: $${cov:-0}% (minimum: $(INTEGRATION_MIN_COV)%)"; \
	awk -v cov="$${cov:-0}" -v min="$(INTEGRATION_MIN_COV)" 'BEGIN { if (cov + 0 < min + 0) { printf "FAIL: integration coverage %.2f%% is below minimum %.2f%%\n", cov, min; exit 1 } }'

coverage:
	go test -race -covermode=atomic -coverpkg=./internal/... -coverprofile=$(TOTAL_COV_FILE) ./...
	go tool cover -html=$(TOTAL_COV_FILE) -o $(COV_HTML_FILE)
	@go tool cover -func=$(TOTAL_COV_FILE) | tail -n 1

lint:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { echo "Error: golangci-lint not found in PATH"; exit 1; }
	$(GOLANGCI_LINT) run ./...

setup-hooks:
	git config core.hooksPath .githooks
	@if [ -d .githooks ]; then chmod +x .githooks/* 2>/dev/null || true; fi
	@echo "Git hooks configured to use .githooks"

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

clean:
	rm -rf $(BIN_DIR) $(UNIT_COV_FILE) $(INTEGRATION_COV_FILE) $(TOTAL_COV_FILE) $(COV_HTML_FILE)
