# Этап 1: Сборка бинарника
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Сначала копируем только go.mod и go.sum, чтобы Docker кэшировал скачанные библиотеки
COPY go.mod go.sum ./
RUN go mod download

# Копируем исходники (лишнее отсекается через .dockerignore) и собираем статический бинарник
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bot ./cmd/bot

# Этап 2: Минимальный защищенный образ для запуска
FROM alpine:3.21

RUN apk --no-cache add ca-certificates tzdata && \
    adduser -D -g '' appuser

WORKDIR /app
COPY --from=builder /out/bot /app/bot

USER appuser

ENTRYPOINT ["/app/bot"]