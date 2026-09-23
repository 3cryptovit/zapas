# syntax=docker/dockerfile:1

# --- сборка ---
FROM golang:1.26 AS build
WORKDIR /src

# Слой зависимостей кэшируется отдельно от кода.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/app ./cmd/app

# --- запуск ---
# distroless: ни шелла, ни пакетного менеджера; процесс не от root (§13.2).
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/app /app/app
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/app/app"]
CMD ["api"]
