FROM mariadb:10.7

COPY backup restore /usr/local/bin/
RUN chmod +x /usr/local/bin/* && \
    mkdir /backup

ENV MEDIAWIKI_BACKUP_VERSION=1.1.0

ENTRYPOINT [ "" ]
