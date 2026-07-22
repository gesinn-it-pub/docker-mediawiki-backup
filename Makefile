
# Resolve the GitHub token from a per-identity env var derived from the
# local git identity (e.g. "alexander-gesinn" -> GH_TOKEN_ALEXANDER_GESINN),
# falling back to GH_API_TOKEN for contributors who haven't migrated yet.
GH_USER := $(shell git config user.name)
GH_TOKEN_VAR := GH_TOKEN_$(shell echo $(GH_USER) | tr '[:lower:]-' '[:upper:]_')
GH_TOKEN := $(or $($(GH_TOKEN_VAR)),$(GH_API_TOKEN))
VERSION = `sed -n -e 's/^ENV MEDIAWIKI_BACKUP_VERSION=//p' Dockerfile`

.PHONY: all
all:

.PHONY: release
release: test git-push gh-login
	gh release create $(VERSION)

.PHONY: build
build:
	docker build -t ghcr.io/gesinn-it-pub/mediawiki-backup:test .

.PHONY: test
test: build
	$(MAKE) -C tests

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
