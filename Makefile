VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BASE_LDFLAGS = -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
LDFLAGS      = -s -w $(BASE_LDFLAGS)
WIKI_ADDR   ?= 127.0.0.1:8080
WIKI_DB     ?= ccg.db
WIKI_REPO   ?= .
WIKI_TOKEN  ?=
HOST_GOOS   := $(shell go env GOOS)
CONTAINER_ARCH ?= $(shell go env GOARCH)

.PHONY: build release build-debug build-json install vet test test-integration-helpers search-eval search-eval-capture intent-eval intent-eval-postgres intent-eval-capture wiki-build wiki-db wiki-docs wiki-run wiki-run-indexed container-artifacts clean

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

# Prints the `search` scoreboard over the golden queries. Asserts nothing; the
# regression checks that fail a build already run as part of `make test`.
search-eval:
	CGO_ENABLED=1 go test -tags "fts5" ./internal/app/search/rank/ -run TestGolden_Report -v -count=1

# Recaptures the candidate fixture from ./ccg.db, which `make wiki-db` builds.
# Only needed when candidate retrieval itself changes; see
# internal/app/search/rank/testdata/README.md before committing the result.
search-eval-capture:
	CGO_ENABLED=1 go test -tags "fts5" ./internal/adapters/outbound/searchsql/ -run TestCaptureGoldenCandidates -capture-golden -count=1

# Prints the find_by_intent scoreboard: how often a plain-language question came
# back with somewhere worth starting, and how much of the code had a recorded
# reason at all. Asserts nothing; TestGoldenIntent_HasNotRegressed is what fails
# a build, and it runs as part of `make test`.
intent-eval:
	CGO_ENABLED=1 go test -tags "fts5" ./internal/adapters/outbound/searchsql/ -run TestGoldenIntent_Report -v -count=1

# Recaptures the annotated corpus from ./ccg.db, which `make wiki-db` builds, and
# re-records the baseline against it. Both steps together: a corpus that moved
# without a re-recorded baseline fails the ratchet for reasons that have nothing
# to do with the code. See internal/app/search/rank/testdata/README.md.
# The same scoreboard on PostgreSQL, which is what a deployed server runs.
# SQLite orders by bm25 and PostgreSQL by ts_rank, so the two scores differ for
# reasons that have nothing to do with the intent code; this is how that gap is
# read rather than guessed at. Needs a throwaway database — TEST_POSTGRES_DSN
# defaults to localhost:5432/ccg_test and the run drops its `public` schema.
intent-eval-postgres:
	CGO_ENABLED=1 go test -tags "fts5,postgres" ./internal/adapters/outbound/searchsql/ -run TestGoldenIntentPostgres_Report -v -count=1

intent-eval-capture:
	CGO_ENABLED=1 go test -tags "fts5" ./internal/adapters/outbound/searchsql/ -run TestCaptureIntentCorpus -capture-intent -count=1
	CGO_ENABLED=1 go test -tags "fts5" ./internal/adapters/outbound/searchsql/ -run TestGoldenIntent_HasNotRegressed -update-intent -count=1

wiki-build:
	cd web/wiki && npm ci && npm run build

container-artifacts: wiki-build
	mkdir -p container-artifacts/$(CONTAINER_ARCH) container-artifacts/wiki
	if [ "$(HOST_GOOS)" = "linux" ]; then \
		$(MAKE) release; \
	else \
		docker run --rm --platform linux/$(CONTAINER_ARCH) \
			--user "$$(id -u):$$(id -g)" \
			-e GOCACHE=/tmp/go-build \
			-e VERSION="$(VERSION)" \
			-e COMMIT="$(COMMIT)" \
			-e DATE="$(DATE)" \
			-v "$$(pwd):/src" -w /src golang:1.25-bookworm \
			sh -c 'CGO_ENABLED=1 go build -tags "fts5" -ldflags "-s -w -X main.version=$$VERSION -X main.commit=$$COMMIT -X main.date=$$DATE" -o ccg ./cmd/ccg/ && CGO_ENABLED=1 go build -tags "fts5" -ldflags "-s -w -X main.version=$$VERSION -X main.commit=$$COMMIT -X main.date=$$DATE" -o ccg-server ./cmd/ccg-server/'; \
	fi
	cp ccg container-artifacts/$(CONTAINER_ARCH)/ccg
	cp ccg-server container-artifacts/$(CONTAINER_ARCH)/ccg-server
	cp -R web/wiki/dist/. container-artifacts/wiki/

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
