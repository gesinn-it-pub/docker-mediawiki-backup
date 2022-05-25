VERSION = `sed -n -e 's/^ENV MEDIAWIKI_BACKUP_VERSION=//p' Dockerfile`

.PHONY: all
all:

.PHONY: release
release: login
	git diff --quiet || (echo 'git directory has changes'; exit 1)
	git push
	gh release create $(VERSION)

.PHONY: build
build:
	docker build -t ghcr.io/gesinn-it/mediawiki-backup:dev .

.PHONY: login
login:
ifndef GH_API_TOKEN
	$(error GH_API_TOKEN is not set)
endif
	git fetch # make sure we have access to the repository
	gh config set prompt disabled
	@echo $(GH_API_TOKEN) | gh auth login --with-token
