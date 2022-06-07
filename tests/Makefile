.PHONY: test
test:
	docker-compose pull
	docker-compose up -d --build
	
	docker-compose run --rm wait-for-wiki-1
	docker-compose run --rm backup-1 create

	docker-compose run --rm wait-for-wiki-2
	docker-compose run --rm backup-2 restore

	docker-compose down --volumes --remove-orphans
