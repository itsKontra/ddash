PNPM := npx -y pnpm@10.34.7

.PHONY: all install generate check-generated lint-proto test vet typecheck check

all: check

# Install the pnpm workspace (needed once before `make generate`).
install:
	$(PNPM) install --frozen-lockfile

# Regenerate Go and TS from every .proto. Generated code is committed.
generate:
	go tool buf generate

# Fail when committed generated code differs from what `make generate` produces.
GENERATED := '*.pb.go' packages/proto/src

check-generated: generate
	@drift="$$(git status --porcelain -- $(GENERATED))"; \
	if [ -n "$$drift" ]; then \
		echo "$$drift"; \
		echo "generated code is stale: run 'make generate' and commit the result" >&2; \
		exit 1; \
	fi

lint-proto:
	go tool buf lint

# Dev-build code (-tags dev) is tested too; release builds never contain it.
test:
	go test ./...
	go test -tags dev ./core/auth/...

vet:
	go vet ./...

typecheck:
	$(PNPM) typecheck

check: lint-proto check-generated vet test typecheck
