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

### service

The call
```shell
> docker-compose run backup docker-mediawiki-backup
```

will start a running service, that automatically runs daily, weekly and monthly backups by invoking the create script. 

The service can be configured by mounting a config.json into the root of the container.

This is an example of a config.json:
```json
{
  "backups": {
    "daily": {
      "time": "02:00",
      "filesystem": {
        "retainCount": 2
      },
      "restic": {
        "retainCount": 7
      }
    },
    "weekly": {
      "time": "02:00",
      "dayOfWeek": "Sunday",
      "filesystem": {
        "retainCount": 4
      }
    },
    "monthly": {
      "time": "02:00",
      "dayOfMonth": 1,
      "restic": {
        "retainCount": 12
      }
    }
  },
  "backupDirectory": "/backup",
  "minStoragePercentage": 10
}
```

Omitting `filesystem` or `restic` from a schedule disables that storage target for the plan. The legacy `retainCount` directly on the schedule is still supported as a shorthand for filesystem-only.

For restic backups, configure the repository and credentials via environment variables:
```yml
environment:
  RESTIC_REPOSITORY: s3:https://fsn1.your-objectstorage.com/my-bucket
  RESTIC_PASSWORD: <your-encryption-password>
  AWS_ACCESS_KEY_ID: <your-access-key>
  AWS_SECRET_ACCESS_KEY: <your-secret-key>
```

To implement the service in your docker-compose stack, add a new / modify the existing implementation too look like this:
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
    entrypoint: /usr/local/bin/docker-mediawiki-backup
    volumes:
      # folder to hold the backup file
      - ./backup:/backup
      - <wiki-images volume or bind mount>:/var/www/html/images # as in [1]
      - ./config.json:/config.json
    environment:
      MYSQL_HOST: mysql                                         # as in [2]
      MYSQL_ROOT_PASSWORD: database                             # as in [3]
      # the desired target owner of the backup folder
      OWNER: ${OWNER:-1000}
  # ...
```




#### Command line arguments
```
  gh-asset <REPOSITORY> <ASSET-NAME> [<VERSION>]
      download asset ASSET-NAME from GitHub REPOSITORY release VERSION (default: latest); be sure to pass an appropriate Github token
      to the docker-compose run command via -e GH_API_TOKEN=<your token>
```


## Releasing

Set the version in `ENV MEDIAWIKI_BACKUP_VERSION=...` in `Dockerfile`, commit and run `make release`.
