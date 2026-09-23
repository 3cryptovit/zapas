# Запускает go-команду в контейнере golang:1.26 — локальный тулчейн не нужен.
#   .\scripts\go.ps1 test ./... -race
param([Parameter(ValueFromRemainingArguments = $true)] $GoArgs)
$root = (Resolve-Path "$PSScriptRoot\..").Path
# Интеграционные тесты ходят в postgres и redis из deploy/docker-compose.yml.
# Если сеть не поднята, контейнер просто не найдёт хосты, а тесты пропустятся.
$net = "zapas_default"
docker run --rm `
  -v "${root}:/src" `
  -v zapas-gomod:/go/pkg/mod `
  -v zapas-gocache:/root/.cache/go-build `
  -w /src `
  -e GOFLAGS=-buildvcs=false `
  -e "TEST_DATABASE_ADMIN_URL=postgres://postgres:postgres@postgres:5432/postgres?sslmode=disable" `
  -e "TEST_REDIS_ADDR=redis:6379" `
  --network $net `
  golang:1.26 go @GoArgs
exit $LASTEXITCODE
