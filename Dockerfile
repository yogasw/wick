FROM golang:1.23 as builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -o binary main.go

FROM alpine:3
RUN apk update && apk add --no-cache ca-certificates tzdata && update-ca-certificates
COPY --from=builder /app/binary .