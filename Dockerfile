FROM golang:1.26.1-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o rate-limiter ./cmd

FROM alpine:3.21

RUN apk --no-cache add ca-certificates tzdata \
    && addgroup -S appgroup \
    && adduser  -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /build/rate-limiter .
COPY --from=builder /build/config.yaml .

RUN chown -R appuser:appgroup /app

USER appuser

EXPOSE 8080

CMD ["./rate-limiter"]
