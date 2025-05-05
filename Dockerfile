# Сборка Go-бинарника
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o bot .

# Лёгкий образ для запуска
FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/bot .
EXPOSE 8081
CMD ["./bot"]
