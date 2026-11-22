FROM golang:1.27 AS builder
COPY ./backup-service /build
WORKDIR /build
RUN go build .


# restic from its official image instead of the (old) distribution package. Dependabot keeps this line current.
FROM restic/restic:0.20.0@sha256:93999df80d4ac6cb148e82d2ef1fe6a81996f17ff535e772723538211b31555a AS restic


FROM mariadb:10.10

RUN apt update && \
    apt install bzip2 curl -y && \
    curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list > /dev/null && \
    apt update && \
    apt install gh -y && \
    rm -rf /var/lib/apt/lists/*

COPY create create-logs restore repair /usr/local/bin/

COPY --from=builder /build/docker-mediawiki-backup /usr/local/bin/
COPY --from=restic /usr/bin/restic /usr/local/bin/restic

RUN chmod +x /usr/local/bin/* && \
    ln -s /usr/local/bin/create /usr/local/bin/backup && \
    mkdir /backup && \
    restic version

ENV MEDIAWIKI_BACKUP_VERSION=2.7.0

ENTRYPOINT [ "" ]
