
# Resolve the GitHub token from a per-identity env var derived from the
# local git identity (e.g. "alexander-gesinn" -> GH_TOKEN_ALEXANDER_GESINN),
# falling back to GH_API_TOKEN for contributors who haven't migrated yet.
GH_USER := $(shell git config user.name)
GH_TOKEN_VAR := GH_TOKEN_$(shell echo $(GH_USER) | tr '[:lower:]-' '[:upper:]_')
GH_TOKEN := $(or $($(GH_TOKEN_VAR)),$(GH_API_TOKEN))
VERSION = `sed -n -e 's/^ENV MEDIAWIKI_BACKUP_VERSION=//p' Dockerfile`

# Go toolchain: single source of truth is the builder stage of the Dockerfile
# (kept current by Dependabot). Override to test other versions, e.g.
# "make test-unit GO_VERSION=1.26".
GO_VERSION ?= $(shell sed -n 's/^FROM golang:\([0-9.]*\) AS builder/\1/p' Dockerfile)
GO_RUN = docker run --rm -v "$(CURDIR)/backup-service":/src -w /src golang:$(GO_VERSION)

show-current-target = @echo; echo "======= $@ ========"

.PHONY: all
all:

.PHONY: release
release: ci git-push gh-login
	gh release create $(VERSION)

.PHONY: build
build:
	docker build -t ghcr.io/gesinn-it-pub/mediawiki-backup:test .

# ======== Lint / Test ========

.PHONY: lint
lint:
	$(show-current-target)
	$(GO_RUN) sh -c 'test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: files need formatting (run make fmt)"; exit 1; }'
	$(GO_RUN) go vet ./...

.PHONY: fmt
fmt:
	$(GO_RUN) gofmt -w .

.PHONY: test-unit
test-unit:
	$(show-current-target)
	@echo "Go $(GO_VERSION)"
	$(GO_RUN) go test -race -cover ./...

.PHONY: test-integration
test-integration: build
	$(show-current-target)
	$(MAKE) -C tests test; rc=$$?; $(MAKE) -C tests clean; exit $$rc

.PHONY: test
test: test-unit test-integration

# ======== CI ========

# Exactly what CI runs; keep CI workflows calling only make targets.
# ci-unit is the cheap leg for the Go version matrix, ci adds the integration test.
.PHONY: ci-unit
ci-unit: lint test-unit

.PHONY: ci
ci: lint test-unit test-integration

# ======== Release ========

.PHONY: git-push
git-push:
	git diff --quiet || (echo 'git directory has changes'; exit 1)
	git push

.PHONY: gh-login
gh-login:
ifndef GH_TOKEN
	$(error No GitHub token found. Set $(GH_TOKEN_VAR) (derived from git config user.name "$(GH_USER)") or GH_API_TOKEN as a fallback.)
endif
	gh config set prompt disabled
	@echo $(GH_TOKEN) | gh auth login --with-token
