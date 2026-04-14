# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -tags "fts5" -ldflags="-s -w" -o /usr/local/bin/ccg ./cmd/ccg/

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates git

COPY --from=builder /usr/local/bin/ccg /usr/local/bin/ccg

WORKDIR /workspace
EXPOSE 8080

ENTRYPOINT ["ccg"]
CMD ["serve", "--transport", "streamable-http", "--http-addr", ":8080"]
