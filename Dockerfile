FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/devops-worker .

FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /out/devops-worker /app/devops-worker
ENV DATA_DIR=/app/data WEB_ADDR=:8080 APP_TIMEZONE=Asia/Shanghai
VOLUME ["/app/data"]
EXPOSE 8080
ENTRYPOINT ["/app/devops-worker"]
