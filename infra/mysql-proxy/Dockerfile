FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY infra/mysql-proxy/main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o mysql-proxy .

FROM alpine:3.19
COPY --from=builder /src/mysql-proxy /mysql-proxy
EXPOSE 3308 8081
ENTRYPOINT ["/mysql-proxy"]
