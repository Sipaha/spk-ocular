.PHONY: kind-up kind-down test-kind e2e-kind dind-up dind-down test-dind e2e-dind build build-web build-go build-desktop release run run-browser test test-go test-web test-e2e lint lint-go lint-web check fmt tidy clean pss

BIN_DIR := build/bin
BIN     := $(BIN_DIR)/spk-ocular
DIST    := cmd/spk-ocular/dist
DESKTOP_TAGS := wails gtk3
# Local builds carry a UTC build time (to the second) and source revision.
# Packaging supplies VERSION explicitly and retains the exact release version.
LOCAL_BUILD_TIME := $(shell date -u +%Y%m%dT%H%M%SZ)
LOCAL_BUILD_REV := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)+$(LOCAL_BUILD_TIME).$(LOCAL_BUILD_REV)
LDFLAGS := -w -s -X main.version=$(VERSION)
PORT ?= 5190
RELEASE_VERSION ?= $(shell cat VERSION)
ARCH ?= $(shell go env GOARCH)

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
	python3 packaging/licenses.py

.PHONY: licenses
licenses: build-web

# Browser-mode binary: pure Go (modernc SQLite), no cgo.
build-go: build-web
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
	$(call with_dist,CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS) production" -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/spk-ocular-release.tmp ./cmd/spk-ocular)
	mv -f $(BIN_DIR)/spk-ocular-release.tmp $(BIN_DIR)/spk-ocular-release

run: build-desktop
	$(BIN_DIR)/spk-ocular-desktop

# Browser mode on http://127.0.0.1:$(PORT) with the real kubeconfig and data dir.
run-browser: build
	$(BIN) --browser --port=$(PORT)

test: test-go test-web test-e2e test-packaging

test-go:
	go test -race -timeout 180s ./...

test-web:
	cd web && pnpm test

.PHONY: test-e2e-prepare test-e2e-targets test-e2e-synth test-e2e-memo check-core
test-e2e: build test-e2e-targets test-e2e-synth test-e2e-memo

# CI supplies the same browser binary to separate native jobs. Local check
# still builds it first and runs every suite, with each suite's own fixtures.
test-e2e-prepare:
	cd tests/e2e && pnpm install --frozen-lockfile --silent && pnpm exec playwright install chromium

test-e2e-targets: test-e2e-prepare
	cd tests/e2e && pnpm exec playwright test -c playwright.config.ts && rm -rf .run

test-e2e-synth: test-e2e-prepare
	cd tests/e2e && pnpm exec playwright test -c playwright.synth.config.ts && rm -rf .run

test-e2e-memo: test-e2e-prepare
	cd tests/e2e && pnpm exec playwright test -c playwright.memo.config.ts && rm -rf .run

# Together with all three prepared browser suites, this is the complete gate.
check-core: lint test-go test-web test-packaging build build-desktop

lint: lint-go lint-web lint-workflows

lint-go:
	go vet ./...
	go vet -tags "$(DESKTOP_TAGS)" ./...
	GOOS=windows CGO_ENABLED=0 go vet -tags wails ./...
	GOOS=darwin CGO_ENABLED=0 go vet ./...
	golangci-lint run
	golangci-lint run --build-tags "$(DESKTOP_TAGS)"

lint-web:
	cd web && pnpm lint

# The gate before every commit: lint (gofmt included), all tests, both builds.
ifeq ($(CHECK_PARALLEL),1)
check:
	python3 scripts/check-parallel.py
else
check: lint test build-desktop
endif

fmt:
	gofmt -w cmd internal

tidy:
	go mod tidy

clean:
	rm -rf build web/dist

pss:
	bash scripts/pss.sh $(PID)

# Disposable test cluster (kind, in Docker): a control plane and a tainted
# worker for drain tests (scripts/kind-config.yaml). The kubeconfig goes to build/,
# never into ~/.kube. KIND=path/to/kind if it is not on PATH.
KIND ?= kind
KIND_CLUSTER ?= ocular-dev
KIND_KUBECONFIG ?= $(CURDIR)/build/kind-$(KIND_CLUSTER).kubeconfig

kind-up:
	mkdir -p build
	$(KIND) get clusters | grep -qx '$(KIND_CLUSTER)' || $(KIND) create cluster --name $(KIND_CLUSTER) --config scripts/kind-config.yaml --kubeconfig $(KIND_KUBECONFIG) --wait 180s
	$(KIND) get kubeconfig --name $(KIND_CLUSTER) > $(KIND_KUBECONFIG)

kind-down:
	$(KIND) delete cluster --name $(KIND_CLUSTER) --kubeconfig $(KIND_KUBECONFIG)
	rm -f $(KIND_KUBECONFIG)

# Real-cluster tests. Fails (not skips) when the cluster is not there.
test-kind:
	@test -s $(KIND_KUBECONFIG) || { echo "no kind kubeconfig at $(KIND_KUBECONFIG): run make kind-up"; exit 1; }
	@kubectl --kubeconfig $(KIND_KUBECONFIG) get --raw /readyz >/dev/null || { echo "kind cluster $(KIND_CLUSTER) is not reachable: run make kind-up"; exit 1; }
	bash scripts/kind-seed.sh $(KIND_KUBECONFIG) >/dev/null
	bash scripts/kind-rbac.sh $(KIND_KUBECONFIG) $(CURDIR)/build/rbac >/dev/null
	bash scripts/kind-metrics.sh $(KIND_KUBECONFIG) >/dev/null
	OCULAR_KIND_KUBECONFIG=$(KIND_KUBECONFIG) OCULAR_KIND_RBAC_DIR=$(CURDIR)/build/rbac go test -race -count=1 -run Kind ./internal/...

# Browser e2e against the real kind cluster (fails without it).
e2e-kind: build
	@test -s $(KIND_KUBECONFIG) || { echo "no kind kubeconfig at $(KIND_KUBECONFIG): run make kind-up"; exit 1; }
	bash scripts/kind-seed.sh $(KIND_KUBECONFIG) >/dev/null
	bash scripts/kind-rbac.sh $(KIND_KUBECONFIG) $(CURDIR)/build/rbac >/dev/null
	bash scripts/kind-metrics.sh $(KIND_KUBECONFIG) >/dev/null
	cd tests/e2e && OCULAR_KIND_KUBECONFIG=$(KIND_KUBECONFIG) OCULAR_KIND_RBAC_DIR=$(CURDIR)/build/rbac pnpm exec playwright test -c playwright.kind.config.ts && rm -rf .run

# Isolated test Docker Engine (docker:29-dind in your docker, 127.0.0.1:23750).
# Only the container recorded in build/ocular-dind.id is ever touched; every
# mutation inside it is preceded by scripts/dind-verify.sh.
dind-up:
	bash scripts/dind-up.sh
	bash scripts/dind-seed.sh

dind-down:
	bash scripts/dind-down.sh

# Real-daemon tests. Fails (not skips) when the test daemon is not there.
test-dind:
	@bash scripts/dind-verify.sh >/dev/null || { echo "the test daemon is not up: run make dind-up"; exit 1; }
	bash scripts/dind-seed.sh >/dev/null
	OCULAR_DIND_HOST=$$(bash scripts/dind-verify.sh) OCULAR_DIND_VERIFY=$(CURDIR)/scripts/dind-verify.sh go test -race -count=1 -run Dind ./internal/...

e2e-dind: build
	@bash scripts/dind-verify.sh >/dev/null || { echo "the test daemon is not up: run make dind-up"; exit 1; }
	bash scripts/dind-seed.sh >/dev/null
	cd tests/e2e && OCULAR_DIND_HOST=$$(bash ../../scripts/dind-verify.sh) OCULAR_DIND_VERIFY=$(CURDIR)/scripts/dind-verify.sh pnpm exec playwright test -c playwright.dind.config.ts && rm -rf .run

.PHONY: package-linux package-windows package-macos test-packaging lint-workflows
package-linux:
	python3 packaging/release.py --version "$(RELEASE_VERSION)" --arch "$(ARCH)" --bin-dir "$(abspath $(BIN_DIR))"

package-windows:
	python3 packaging/portable.py --version "$(RELEASE_VERSION)" --os windows --arch "$(ARCH)"

package-macos:
	python3 packaging/portable.py --version "$(RELEASE_VERSION)" --os darwin --arch "$(ARCH)"

# These checks run without building packages or publishing a release.
test-packaging:
	python3 -m unittest discover -s scripts/tests -v
	python3 -m unittest discover -s packaging/tests -v
	python3 scripts/check-docs.py
	desktop-file-validate packaging/linux/spk-ocular.desktop

lint-workflows:
	actionlint -shellcheck=
	sh -n packaging/linux/update-caches.sh

# Real Helm SDK lifecycle + PostgreSQL in an owned namespace of verified kind.
# Set OCULAR_KIND_KUBECONFIG explicitly to the disposable cluster's kubeconfig.
.PHONY: test-helm-live
test-helm-live:
	python3 scripts/helm-live.py
