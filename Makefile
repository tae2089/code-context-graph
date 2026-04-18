VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG       = github.com/imtaebin/code-context-graph/cmd/ccg
LDFLAGS   = -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build install test clean

build:
	CGO_ENABLED=1 go build -tags "fts5" -ldflags '$(LDFLAGS)' -o ccg ./cmd/ccg/

install:
	CGO_ENABLED=1 go install -tags "fts5" -ldflags '$(LDFLAGS)' ./cmd/ccg/

test:
	CGO_ENABLED=1 go test -tags "fts5" ./...

clean:
	rm -f ccg
