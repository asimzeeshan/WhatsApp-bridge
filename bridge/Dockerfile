FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /whatsapp-bridge .

FROM alpine:3.21

RUN apk add --no-cache ca-certificates sqlite-libs
RUN adduser -D -u 1000 bridge

WORKDIR /app
COPY --from=builder /whatsapp-bridge .

RUN mkdir -p /app/data /app/logs /app/media && chown -R bridge:bridge /app

USER bridge

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/api/status || exit 1

ENTRYPOINT ["./whatsapp-bridge"]
CMD ["--config", "/app/config.toml"]
