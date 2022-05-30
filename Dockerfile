FROM mariadb:10.7

RUN apt update && \
    apt install curl -y && \
    curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list > /dev/null && \
    apt update && \
    apt install gh -y && \
    rm -rf /var/lib/apt/lists/*

COPY backup restore /usr/local/bin/
RUN chmod +x /usr/local/bin/* && \
    mkdir /backup

ENV MEDIAWIKI_BACKUP_VERSION=1.2.0

ENTRYPOINT [ "" ]
