FROM golang:latest AS builder
COPY ./backup-service /build
WORKDIR /build
RUN go build .


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

RUN chmod +x /usr/local/bin/* && \
    ln -s /usr/local/bin/create /usr/local/bin/backup && \
    mkdir /backup

ENV MEDIAWIKI_BACKUP_VERSION=2.2.2

ENTRYPOINT [ "" ]
