PNPM := npx -y pnpm@10.34.7

.PHONY: all install generate check-generated lint-proto lint lint-go lint-web check-plugins build test test-go test-web vet typecheck check dev release-dir image docker-agent-image smoke record smoke-writes

all: check

# Install the pnpm workspace (needed once before `make generate`).
install:
	$(PNPM) install --frozen-lockfile

# Regenerate Go and TS from every .proto, the icon data from the pinned
# lucide-static (packages/ui/scripts/icons.ts) and the App icon names from
# the pinned dashboard-icons (core/icons/dashboard, which downloads them).
# Generated code is committed.
generate:
	go tool buf generate
	node packages/ui/scripts/icons.ts
	go run ./core/icons/dashboard
	go run ./core/icons/gzip

# Fail when committed generated code differs from what `make generate` produces.
GENERATED := '*.pb.go' '*.connect.go' packages/proto/src core/icons/lucide core/icons/dashboard-icons packages/ui/src/icon-names.ts packages/ui/src/dashboard-icons.ts

check-generated: generate
	@drift="$$(git status --porcelain -- $(GENERATED))"; \
	if [ -n "$$drift" ]; then \
		echo "$$drift"; \
		echo "generated code is stale: run 'make generate' and commit the result" >&2; \
		exit 1; \
	fi

lint-proto:
	go tool buf lint

# golangci-lint is pinned here rather than as a go.mod tool, so its many
# dependencies stay out of the module. Config: .golangci.yml.
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

lint: lint-go lint-web

lint-go:
	$(GOLANGCI_LINT) run ./...

# oxlint (.oxlintrc.json) over the workspace, then the Plugin style lint:
# no hard-coded colours, radii or fonts, no arbitrary Tailwind values and
# no design-system import but @ddash/ui (packages/plugin-check).
lint-web:
	$(PNPM) lint

# The Plugin contract check: imports each built bundle through the import
# map (the shell's vendor ESM) and validates what definePlugin returns.
check-plugins: build
	$(PNPM) check-plugins

# Build the web with Vite: the shell and vendor ESM into web/dist/shell
# (embedded by package web, so build before building the core) and each
# Plugin's bundle into plugins/<name>/dist/ (copied into the Release).
#
# Built once: test-go, check-plugins, release-dir, image and dev all need
# it, and it runs again only when a file of the workspace is newer than
# the last build (`make -B build` forces it). The stamp sits in
# node_modules, which is ignored and not embedded.
WEB_SOURCES := $(wildcard $(shell git ls-files -co --exclude-standard -- web packages plugins package.json pnpm-lock.yaml pnpm-workspace.yaml tsconfig.base.json))
WEB_BUILT := node_modules/.ddash-web-built

build: $(WEB_BUILT)

$(WEB_BUILT): $(WEB_SOURCES)
	$(PNPM) -r build
	@touch $@

test: test-go test-web

# Dev-build code (-tags dev) is tested too; release builds never contain it.
# go test ./... includes the subprocess test (it builds the Release and
# runs it against fakelab), the fixture IP guard (internal/ipguard) and the
# embedded shell, so it needs the web built.
test-go: build
	go test ./...
	go test -tags dev ./core/auth/...

# vitest in every workspace package.
test-web:
	$(PNPM) -r test

vet:
	go vet ./...

typecheck:
	$(PNPM) typecheck

check: lint-proto check-generated vet lint test typecheck check-plugins

# Build the Release into dist/ (catalogue.json, SHA256SUMS, one <name>.ct
# per Component, one <name>.ddapp per Plugin), as Release dev unless RELEASE is set,
# for this machine unless PLATFORM (GOOS/GOARCH) is set. The release
# workflow builds RELEASE=<tag> PLATFORM=linux/amd64.
RELEASE ?= dev
PLATFORM ?=
# AGENT_IMAGE_REF (the Docker Agent's image by digest, from
# `make docker-agent-image` and its push) is pinned in the Compose file
# docker-agent-api generates.
AGENT_IMAGE_REF ?=
release-dir: build
	rm -rf dist
	go run ./cmd/release -out dist -release $(RELEASE) $(if $(PLATFORM),-platform $(PLATFORM)) $(if $(AGENT_IMAGE_REF),-agent-image $(AGENT_IMAGE_REF))

# Build the core image $(IMAGE):$(RELEASE) (Dockerfile) from bin/image/: the
# static linux/amd64 core binary, stamped with RELEASE, and an empty data/.
# RELEASE must be a tag here (vMAJOR.MINOR.PATCH): the dev Release isn't on
# GitHub. DOCKER=podman works too.
IMAGE ?= ghcr.io/itskontra/ddash
DOCKER ?= docker
image: build
	rm -rf bin/image
	mkdir -p bin/image/data
	go run ./cmd/release -core bin/image/ddash -release $(RELEASE) -platform linux/amd64
	$(DOCKER) build --platform linux/amd64 $(if $(filter podman,$(DOCKER)),--format docker) \
		-f Dockerfile -t $(IMAGE):$(RELEASE) bin/image

# Build the Docker Agent's image $(AGENT_IMAGE):$(RELEASE)
# (deploy/docker-agent/Dockerfile) from bin/docker-agent/: the static
# linux/amd64 ddash-docker-agent, stamped with RELEASE.
AGENT_IMAGE ?= ghcr.io/itskontra/ddash-docker-agent
docker-agent-image:
	rm -rf bin/docker-agent
	mkdir -p bin/docker-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
		-ldflags "-X github.com/itsKontra/ddash/agent.Release=$(RELEASE)" \
		-o bin/docker-agent/ddash-docker-agent ./cmd/ddash-docker-agent
	$(DOCKER) build --platform linux/amd64 $(if $(filter podman,$(DOCKER)),--format docker) \
		-f deploy/docker-agent/Dockerfile -t $(AGENT_IMAGE):$(RELEASE) bin/docker-agent

# Run everything locally against fakelab: builds dist/, seeds Component
# config, installs every Plugin and runs the core (dev auth) on :8080,
# Plugins rebuilding with `vite build --watch` and the shell on the Vite
# dev server (http://127.0.0.1:5173), which proxies the core.
dev: build
	go run ./cmd/dev $(DEV_ARGS)

# By hand only, never in CI or on a schedule: every system Component's
# getters against the real homelab systems in the gitignored smoke.env
# (copy smoke.env.example), then network's info by address over them,
# checking that they decode and that their invariants hold. Component
# DBs stay in .smoke/ between runs.
smoke:
	go run ./cmd/smoke -env smoke.env -data .smoke

# The same run, also writing every answer as fixtures,
# scrubbed (SCRUB_* in smoke.env plus the gitignored scrub.map), into
# components/<name>/testdata/$(SCENARIO)/. Review them, then move what you
# need into baseline in a PR of its own.
SCENARIO ?= recorded
record:
	go run ./cmd/smoke -env smoke.env -data .smoke -record -scenario $(SCENARIO) -map scrub.map

# Opt-in, by hand only, for a Release that changes an Action: the real
# Actions of pfsense-api, adguard-api, proxmox-api and
# nginx-proxy-manager-api round-trip on the smoke targets only (the
# DDASH_SMOKE alias, a fake-MAC static map and an AdGuard client on
# smoke.env's SMOKE_UNUSED_IP, a Host Override and its alias on
# smoke.env's SMOKE_HOST_OVERRIDE, the stopped ddash-smoke LXC, a Proxy
# Host for SMOKE_PROXY_HOST) plus a refused start on a Protected
# Workload. Cleanup always runs.
smoke-writes:
	go run ./cmd/smoke -writes -env smoke.env -data .smoke
