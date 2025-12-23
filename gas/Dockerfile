# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder
WORKDIR /app

# Build requires CGO for modernc.org/sqlite
RUN apk add --no-cache build-base

COPY go.mod go.sum ./
# 确保依赖与go.mod一致，自动解决go.sum问题
RUN go mod tidy && go mod download

COPY . .
ENV CGO_ENABLED=1
RUN go build -o /app/bin/gasdash .

FROM alpine:3.19
WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/bin/gasdash /app/gasdash
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static

# Default data path is GAS_DB_PATH=/app/data/gas_usage.db
RUN mkdir -p /app/data
VOLUME ["/app/data"]

EXPOSE 8080
ENV GAS_DB_PATH=/app/data/gas_usage.db \
    GAS_SERVER_ADDR=:8080

ENTRYPOINT ["/app/gasdash"]
