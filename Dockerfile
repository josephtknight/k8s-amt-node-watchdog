FROM golang:1.21-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /node-watchdog ./cmd/watchdog

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /node-watchdog /node-watchdog
USER nonroot:nonroot
ENTRYPOINT ["/node-watchdog"]
