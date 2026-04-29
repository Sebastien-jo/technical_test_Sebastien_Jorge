FROM golang:1.26.1-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o rate-limiter ./cmd/server

FROM alpine:3.21

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /build/rate-limiter .
COPY --from=builder /build/config.yaml .

EXPOSE 8080

CMD ["./rate-limiter"]
