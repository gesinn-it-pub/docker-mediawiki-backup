# mediawiki-backup

Docker image to backup/restore MediaWiki instances

## Using with docker-compose

The most convenient way to use mediawiki-backup is in the context of a docker-compose setup.

```yml
# docker-compose.yml
services: 
  
  wiki:
    # ...
    volumes:
      # ...
      - <wiki-images volume or bind mount>:/var/www/html/images # [1]
      #...
    environment:
      # ...
      MYSQL_HOST: mysql                                         # [2]
      # ...

  mysql:
    # ...
    environment:
      # ...
      MYSQL_ROOT_PASSWORD: database                             # [3]
      # ...
  
  backup:
    image: ghcr.io/gesinn-it-pub/mediawiki-backup:latest
    volumes:
      # folder to hold the backup file
      - ./backup:/backup
      - <wiki-images volume or bind mount>:/var/www/html/images # as in [1]
    environment:
      MYSQL_HOST: mysql                                         # as in [2]
      MYSQL_ROOT_PASSWORD: database                             # as in [3]
      # the desired target owner of the backup folder
      OWNER: ${OWNER:-1000}
    profiles:
      - no-up # don't start on 'docker-compose up'
  # ...
```

### backup

The call
```shell
> docker-compose run --rm backup create
```
will 
* delete a possibly existing `./backup/mediawiki-backup.tar`,
* create a `./backup/mediawiki-backup.tar` containing
  * `./mysqldump.bz2`, a mysql db dump,
  * `./images`, the wiki images folder
* set the owner of the `./backup` folder to the `OWNER` passed as environment variable

### restore

The call
```shell
> docker-compose run --rm backup restore
```
will 
* delete all files within `/var/www/html/images`,
* restore `/var/www/html/images` and the mysql db according to the contents of `./backup/mediawiki-backup.tar`

To be sure, the restored wiki db contains all changes required by possible local extensions, execute 
```shell
> docker-compose exec wiki php maintenance/update.php --quick
```

As the Elasticsearch server database is not backed up, it has to be updated manually by
```shell
> docker-compose exec wiki php extensions/CirrusSearch/maintenance/ForceSearchIndex.php
```

#### Command line arguments
```
  gh-asset <REPOSITORY> <ASSET-NAME> [<VERSION>]
      download asset ASSET-NAME from GitHub REPOSITORY release VERSION (default: latest); be sure to pass an appropriate Github token
      to the docker-compose run command via -e GH_API_TOKEN=<your token>
```


## Releasing

Set the version in `ENV MEDIAWIKI_BACKUP_VERSION=...` in `Dockerfile`, commit and run `make release`.
