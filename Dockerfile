FROM golang:1.27 AS builder
COPY ./backup-service /build
WORKDIR /build
RUN go build .


# restic from its official image instead of the (old) distribution package. Dependabot keeps this line current.
FROM restic/restic:0.19.1@sha256:136600b6ff6843d61d355f7f71f460a166429f35de6fd11b568fece3c9a4d510 AS restic


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

ENV MEDIAWIKI_BACKUP_VERSION=2.5.0

ENTRYPOINT [ "" ]
