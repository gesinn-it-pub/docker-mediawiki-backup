# mediawiki-backup

Docker image to backup/restore MediaWiki instances

## backup

```yml
# docker-compose.yml
services: 
  backup:
    image: ghcr.io/gesinn-it-pub/mediawiki-backup:latest
    volumes:
      - ./backup:/backup
      - wiki-images:/var/www/html/images
    environment:
      MYSQL_HOST: mysql
      MYSQL_ROOT_PASSWORD: database
```

Then a call with
```shell
> docker-compose backup backup
```
will 
* delete all files in `./backup`
* create a `./backup/mediawiki-backup.tar` containing
  * `./mysqldb.bz2`, a mysql db dump,
  * `./images`, the wiki images folder.

## Releasing

Set the version in `ENV MEDIAWIKI_BACKUP_VERSION=...` in `Dockerfile`, commit and run `make release`.
