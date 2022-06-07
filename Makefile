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
ifndef GH_API_TOKEN
	$(error GH_API_TOKEN is not set)
endif
	gh config set prompt disabled
	@echo $(GH_API_TOKEN) | gh auth login --with-token
