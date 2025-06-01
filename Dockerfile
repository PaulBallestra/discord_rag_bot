FROM --platform=linux/amd64 golang:1.24-alpine AS builder

# Install FFmpeg, opus, and build dependencies
RUN apk add --no-cache \
    ffmpeg \
    opus-dev \
    pkgconfig \
    gcc \
    musl-dev \
    git \
    ca-certificates

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o bot ./cmd/bot

FROM --platform=linux/amd64 alpine:latest
RUN apk add --no-cache \
    ffmpeg \
    opus \
    ca-certificates \
    tzdata

# Create app directory
WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /app/bot .

# Make sure the binary is executable
RUN chmod +x ./bot

CMD ["./bot"]
