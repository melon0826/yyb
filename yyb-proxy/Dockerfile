FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -o /yyb-proxy .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && update-ca-certificates
ENV TZ=Asia/Shanghai
COPY --from=builder /yyb-proxy .
EXPOSE 1080
ENTRYPOINT ["./yyb-proxy"]
