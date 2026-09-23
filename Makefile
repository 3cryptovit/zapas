# Локальный Go-тулчейн не обязателен: цели с префиксом docker- всё делают
# в контейнере golang:1.26 (см. scripts/go.ps1).
SHELL := /bin/bash
COMPOSE := docker compose -f deploy/docker-compose.yml
GO_IMAGE := golang:1.26
GO_RUN := docker run --rm -v "$(CURDIR)":/src -v zapas-gomod:/go/pkg/mod \
	-v zapas-gocache:/root/.cache/go-build -w /src -e GOFLAGS=-buildvcs=false $(GO_IMAGE)

.PHONY: help
help: ## Показать список целей
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## --- окружение ---

.PHONY: dev
dev: up migrate ## Поднять инфраструктуру и применить миграции
	@echo "postgres :5432 · redis :6379 · mailpit http://localhost:8025"

.PHONY: up
up: ## Поднять postgres, redis и mailpit
	$(COMPOSE) --profile dev up -d

.PHONY: down
down: ## Остановить окружение
	$(COMPOSE) --profile dev down

.PHONY: reset-db
reset-db: ## Снести том БД и накатить схему заново
	$(COMPOSE) down -v
	$(MAKE) up
	sleep 5
	$(MAKE) migrate

## --- база ---

.PHONY: migrate
migrate: ## Применить миграции (goose up)
	pwsh -File scripts/app.ps1 migrate up

.PHONY: migrate-status
migrate-status: ## Показать состояние миграций
	pwsh -File scripts/app.ps1 migrate status

.PHONY: sqlc
sqlc: ## Сгенерировать код из queries/
	docker run --rm -v "$(CURDIR)":/src -w /src sqlc/sqlc:latest generate

## --- код ---

.PHONY: build
build: ## Собрать бинарник
	$(GO_RUN) go build -o bin/app ./cmd/app

.PHONY: test
test: ## Юнит-тесты с детектором гонок
	$(GO_RUN) go test $(PKGS) -race -count=1

.PHONY: test-int
test-int: ## Интеграционные тесты (testcontainers, нужен Docker)
	$(GO_RUN) go test $(PKGS) -race -count=1 -tags=integration

.PHONY: cover
cover: ## Покрытие по пакетам ядра
	$(GO_RUN) go test ./internal/forecast/... ./internal/replenishment/... ./internal/clock/... \
		./internal/platform/qty/... -coverprofile=coverage.out
	$(GO_RUN) go tool cover -func=coverage.out | tail -1

.PHONY: lint
lint: ## golangci-lint + go vet
	$(GO_RUN) go vet $(PKGS)
	docker run --rm -v "$(CURDIR)":/src -w /src golangci/golangci-lint:v2.13.2 golangci-lint run --timeout 5m

.PHONY: fmt
fmt: ## gofmt -w
	$(GO_RUN) gofmt -l -w ./cmd ./internal

.PHONY: tidy
tidy: ## go mod tidy
	$(GO_RUN) go mod tidy

.PHONY: seed
seed: ## Создать локального тенанта для разработки
	pwsh -File scripts/app.ps1 seed

.PHONY: api-lint
api-lint: ## Проверить контракт OpenAPI
	npx --yes @redocly/cli@1.34.2 lint --config docs/api/redocly.yaml docs/api/openapi.yaml

.PHONY: perf-fixture
perf-fixture: ## Наполнить локальную базу: 300 позиций, 1 млн движений
	pwsh -File scripts/app.ps1 seed -email perf@zapas.local -password perf-parol-42 -name Нагрузка
	docker compose -f deploy/docker-compose.yml exec -T postgres \
		psql -U postgres -d zapas -v ON_ERROR_STOP=1 < tests/load/fixture.sql

.PHONY: perf
perf: ## Нагрузочный прогон k6 (только локально, не против прода)
	docker run --rm --network zapas_default -v "$(CURDIR)":/src -w /src \
		-e BASE_URL=http://host.docker.internal:8080 \
		grafana/k6:latest run /src/tests/load/api.js

.PHONY: deploy
deploy: ## Выкатить на прод (нужен ZAPAS_SSH_KEY)
	bash scripts/deploy.sh
