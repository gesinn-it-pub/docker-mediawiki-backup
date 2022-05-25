VERSION = `sed -n -e 's/^ENV MEDIAWIKI_BACKUP_VERSION=//p' Dockerfile`

.PHONY: test
test:
	docker-compose pull
	docker-compose up -d --build
	
	docker-compose run --rm wait-for-wiki-1
	docker-compose run --rm backup-1 backup

	docker-compose run --rm wait-for-wiki-2
	docker-compose run --rm backup-2 restore

	docker-compose down --volumes --remove-orphans
