# Запускает режим приложения в контейнере с подключением к локальному compose.
#   .\scripts\app.ps1 migrate up
param([Parameter(ValueFromRemainingArguments = $true)] $AppArgs)
$root = (Resolve-Path "$PSScriptRoot\..").Path
$net = "zapas_default"
$host_ = "postgres"
$redis = "redis"
docker run --rm `
  -v "${root}:/src" `
  -v zapas-gomod:/go/pkg/mod `
  -v zapas-gocache:/root/.cache/go-build `
  -w /src `
  -e GOFLAGS=-buildvcs=false `
  -e APP_ENV=dev `
  -e "DATABASE_URL=postgres://zapas_app:zapas_app@${host_}:5432/zapas?sslmode=disable" `
  -e "MIGRATE_DATABASE_URL=postgres://zapas_owner:zapas_owner@${host_}:5432/zapas?sslmode=disable" `
  -e "MAINT_DATABASE_URL=postgres://zapas_maint:zapas_maint@${host_}:5432/zapas?sslmode=disable" `
  -e "REDIS_ADDR=${redis}:6379" `
  --network $net `
  golang:1.26 go run ./cmd/app @AppArgs
exit $LASTEXITCODE
