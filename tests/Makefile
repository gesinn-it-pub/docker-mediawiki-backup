RESTORE_OWNER = www-data.
COMPOSE ?= docker compose

.PHONY: test
test:
	# setup
	$(COMPOSE) pull
	$(COMPOSE) up -d --build
	$(COMPOSE) run --rm wait-for-wiki-1
	$(COMPOSE) run --rm wait-for-wiki-2

	# arrange
	$(COMPOSE) exec -T wiki-1 bash -c "echo some test text | php maintenance/edit.php Test"
	$(COMPOSE) exec -T wiki-1 bash -c "mkdir -p /tmp/image-test; cp resources/assets/mediawiki.png /tmp/image-test; php maintenance/importImages.php /tmp/image-test"

	# act
	$(COMPOSE) run --rm backup-1 create
	$(COMPOSE) run -e RESTORE_OWNER=${RESTORE_OWNER} --rm backup-2 restore

	# assert
	$(COMPOSE) exec -T wiki-2 bash -c "php maintenance/view.php Test"
	$(COMPOSE) exec -T wiki-2 bash -c "php maintenance/view.php File:Mediawiki.png"

.PHONY: clean
clean:
	$(COMPOSE) down --volumes --remove-orphans
	rm -rf backup
