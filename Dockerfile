FROM golang:1.21-alpine AS builder

# Install FFmpeg and other dependencies
RUN apk add --no-cache ffmpeg opus-dev pkgconfig gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o bot ./cmd/bot

FROM alpine:latest
RUN apk add --no-cache ffmpeg opus ca-certificates
WORKDIR /root/
COPY --from=builder /app/bot .
CMD ["./bot"] 