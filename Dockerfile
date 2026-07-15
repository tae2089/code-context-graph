# Wiki UI build stage
FROM node:22-alpine AS wiki-builder

WORKDIR /src/web/wiki
COPY web/wiki/package.json web/wiki/package-lock.json ./
RUN npm ci
COPY web/wiki/ ./
RUN npm run build

# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev git

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -tags "fts5" -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o /usr/local/bin/ccg ./cmd/ccg/ \
    && CGO_ENABLED=1 go build -tags "fts5" -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o /usr/local/bin/ccg-server ./cmd/ccg-server/

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates git \
    && addgroup -S ccg \
    && adduser -S -G ccg -h /home/ccg ccg \
    && mkdir -p /repo /repos /home/ccg/.cache /home/ccg/.config/ccg \
    && chown -R ccg:ccg /repo /repos /home/ccg

COPY --from=builder /usr/local/bin/ccg /usr/local/bin/ccg
COPY --from=builder /usr/local/bin/ccg-server /usr/local/bin/ccg-server
COPY --from=wiki-builder /src/web/wiki/dist /usr/share/ccg/wiki

WORKDIR /repo
USER ccg
ENV HOME=/home/ccg \
    XDG_CACHE_HOME=/home/ccg/.cache \
    CCG_REPO_ROOT=/repos
EXPOSE 8080

ENTRYPOINT ["ccg-server"]
CMD ["--http-addr", ":8080", "--wiki-dir", "/usr/share/ccg/wiki"]
