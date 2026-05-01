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

RUN apk add --no-cache ca-certificates git \
    && addgroup -S ccg \
    && adduser -S -G ccg -h /home/ccg ccg \
    && mkdir -p /workspace /repos /home/ccg/.cache /home/ccg/.config/ccg \
    && chown -R ccg:ccg /workspace /repos /home/ccg

COPY --from=builder /usr/local/bin/ccg /usr/local/bin/ccg

WORKDIR /workspace
USER ccg
ENV HOME=/home/ccg \
    XDG_CACHE_HOME=/home/ccg/.cache \
    CCG_REPO_ROOT=/repos
EXPOSE 8080

ENTRYPOINT ["ccg"]
CMD ["serve", "--transport", "streamable-http", "--http-addr", ":8080"]
