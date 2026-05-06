VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BASE_LDFLAGS = -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
LDFLAGS      = -s -w $(BASE_LDFLAGS)
WIKI_ADDR   ?= 127.0.0.1:8080
WIKI_DB     ?= ccg.db
WIKI_REPO   ?= .
WIKI_TOKEN  ?=

.PHONY: build release build-debug build-json install vet test test-integration-helpers wiki-build wiki-db wiki-docs wiki-run wiki-run-indexed clean

build: release

release:
	CGO_ENABLED=1 go build -tags "fts5" -ldflags '$(LDFLAGS)' -o ccg ./cmd/ccg/
	CGO_ENABLED=1 go build -tags "fts5" -ldflags '$(LDFLAGS)' -o ccg-server ./cmd/ccg-server/

build-debug:
	CGO_ENABLED=1 go build -tags "fts5" -ldflags '$(BASE_LDFLAGS)' -o ccg ./cmd/ccg/
	CGO_ENABLED=1 go build -tags "fts5" -ldflags '$(BASE_LDFLAGS)' -o ccg-server ./cmd/ccg-server/

build-json:
	CGO_ENABLED=1 go build -json -tags "fts5" ./... > build-results.json

install:
	CGO_ENABLED=1 go install -tags "fts5" -ldflags '$(LDFLAGS)' ./cmd/ccg/ ./cmd/ccg-server/

vet:
	go vet ./...

test: test-integration-helpers
	CGO_ENABLED=1 go test -tags "fts5" ./...
	bash ./scripts/integration-test-helpers_test.sh

test-integration-helpers:
	bash ./scripts/integration-test-helpers_test.sh

wiki-build:
	cd web/wiki && npm ci && npm run build

wiki-db:
	CGO_ENABLED=1 go run -tags "fts5" ./cmd/ccg --db-driver sqlite --db-dsn '$(WIKI_DB)' migrate
	CGO_ENABLED=1 go run -tags "fts5" ./cmd/ccg --db-driver sqlite --db-dsn '$(WIKI_DB)' build '$(WIKI_REPO)'

wiki-docs: wiki-db
	CGO_ENABLED=1 go run -tags "fts5" ./cmd/ccg --db-driver sqlite --db-dsn '$(WIKI_DB)' docs --out docs

wiki-run: wiki-build wiki-db
	CGO_ENABLED=1 go run -tags "fts5" ./cmd/ccg-server --db-driver sqlite --db-dsn '$(WIKI_DB)' --http-addr '$(WIKI_ADDR)' --http-bearer-token '$(WIKI_TOKEN)' --wiki-dir web/wiki/dist

wiki-run-indexed: wiki-build wiki-docs
	CGO_ENABLED=1 go run -tags "fts5" ./cmd/ccg-server --db-driver sqlite --db-dsn '$(WIKI_DB)' --http-addr '$(WIKI_ADDR)' --http-bearer-token '$(WIKI_TOKEN)' --wiki-dir web/wiki/dist

clean:
	rm -f ccg ccg-server
