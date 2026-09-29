.PHONY: kind-up kind-down test-kind build build-web build-go build-desktop release run run-browser test test-go test-web test-e2e lint lint-go lint-web check fmt tidy clean pss

BIN_DIR := build/bin
BIN     := $(BIN_DIR)/spk-ocular
DIST    := cmd/spk-ocular/dist
DESKTOP_TAGS := wails gtk3
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -w -s -X main.version=$(VERSION)
PORT ?= 5190

# The SPA is embedded from cmd/spk-ocular/dist: copied in for the build and
# reset afterwards, so a web build never dirties git.
define with_dist
	rm -rf $(DIST) && mkdir -p $(DIST) && cp -r web/dist/. $(DIST)/
	$(1)
	rm -rf $(DIST) && mkdir -p $(DIST) && touch $(DIST)/.gitkeep
endef

build: build-web build-go

build-web:
	cd web && pnpm install --frozen-lockfile --silent && pnpm build

# Browser-mode binary: pure Go (modernc SQLite), no cgo.
build-go:
	mkdir -p $(BIN_DIR)
	$(call with_dist,CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/spk-ocular)

# Desktop (GTK/WebKit, cgo). Built to a temp file and mv'd into place: a
# running instance keeps its old inode, a failed build keeps the old binary.
build-desktop: build-web
	mkdir -p $(BIN_DIR)
	$(call with_dist,CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS)" -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/spk-ocular-desktop.tmp ./cmd/spk-ocular)
	mv -f $(BIN_DIR)/spk-ocular-desktop.tmp $(BIN_DIR)/spk-ocular-desktop

# Desktop without DevTools.
release: build-web
	mkdir -p $(BIN_DIR)
	$(call with_dist,CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS) production" -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/spk-ocular-release ./cmd/spk-ocular)

run: build-desktop
	$(BIN_DIR)/spk-ocular-desktop

# Browser mode on http://127.0.0.1:$(PORT) with the real kubeconfig and data dir.
run-browser: build
	$(BIN) --browser --port=$(PORT)

test: test-go test-web test-e2e

test-go:
	go test -race -timeout 180s ./...

test-web:
	cd web && pnpm test

test-e2e: build
	cd tests/e2e && pnpm install --frozen-lockfile --silent && pnpm exec playwright install chromium && pnpm exec playwright test && rm -rf .run

lint: lint-go lint-web

lint-go:
	go vet ./...
	go vet -tags "$(DESKTOP_TAGS)" ./...
	golangci-lint run
	golangci-lint run --build-tags "$(DESKTOP_TAGS)"

lint-web:
	cd web && pnpm lint

# The gate before every commit: lint (gofmt included), all tests, both builds.
check: lint test build-desktop

fmt:
	gofmt -w cmd internal

tidy:
	go mod tidy

clean:
	rm -rf build web/dist

pss:
	bash scripts/pss.sh $(PID)

# Disposable test cluster (kind, in Docker). The kubeconfig goes to build/,
# never into ~/.kube. KIND=path/to/kind if it is not on PATH.
KIND ?= kind
KIND_CLUSTER ?= ocular-dev
KIND_KUBECONFIG ?= $(CURDIR)/build/kind-$(KIND_CLUSTER).kubeconfig

kind-up:
	mkdir -p build
	$(KIND) get clusters | grep -qx '$(KIND_CLUSTER)' || $(KIND) create cluster --name $(KIND_CLUSTER) --kubeconfig $(KIND_KUBECONFIG) --wait 120s
	$(KIND) get kubeconfig --name $(KIND_CLUSTER) > $(KIND_KUBECONFIG)

kind-down:
	$(KIND) delete cluster --name $(KIND_CLUSTER)
	rm -f $(KIND_KUBECONFIG)

# Real-cluster tests. Fails (not skips) when the cluster is not there.
test-kind:
	@test -s $(KIND_KUBECONFIG) || { echo "no kind kubeconfig at $(KIND_KUBECONFIG): run make kind-up"; exit 1; }
	@kubectl --kubeconfig $(KIND_KUBECONFIG) get --raw /readyz >/dev/null || { echo "kind cluster $(KIND_CLUSTER) is not reachable: run make kind-up"; exit 1; }
	OCULAR_KIND_KUBECONFIG=$(KIND_KUBECONFIG) go test -race -count=1 -run Kind ./...
