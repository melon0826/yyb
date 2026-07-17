FROM golang:1.23-alpine AS builder

ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /yyb-go ./cmd/yyb-go

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && update-ca-certificates
ENV TZ=Asia/Shanghai

WORKDIR /app
COPY --from=builder /yyb-go .
COPY resource/ ./resource/

COPY entrypoint.sh /
RUN chmod +x /entrypoint.sh

EXPOSE 8000
ENTRYPOINT ["/entrypoint.sh"]
