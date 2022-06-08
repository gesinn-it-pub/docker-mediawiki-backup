.PHONY: test
test:
	# setup
	docker-compose pull
	docker-compose up -d --build
	docker-compose run --rm wait-for-wiki-1
	docker-compose run --rm wait-for-wiki-2
	
	# arrange
	docker-compose exec -T wiki-1 bash -c "echo some test text | php maintenance/edit.php Test"
	docker-compose exec -T wiki-1 bash -c "mkdir -p /tmp/image-test; cp resources/assets/mediawiki.png /tmp/image-test; php maintenance/importImages.php /tmp/image-test"
	
	# act
	docker-compose run --rm backup-1 create
	docker-compose run --rm backup-2 restore

	# assert
	docker-compose exec -T wiki-2 bash -c "php maintenance/view.php Test"
	docker-compose exec -T wiki-2 bash -c "php maintenance/view.php File:Mediawiki.png"

	# tear-down
	# docker-compose down --volumes --remove-orphans
