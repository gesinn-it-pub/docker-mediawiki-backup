FROM gesinn/docker-mediawiki-sqlite:1.35

# add /build-tools and /tools
RUN rm -rf LocalSettings.php /var/www/data && \
    curl -LJ https://github.com/gesinn-it-pub/docker-mediawiki-tools/tarball/1.0.4 -o /tools.tgz && \
    tar -xzf /tools.tgz -C / --strip-components 1 && rm /tools.tgz && \
    echo '#!/bin/bash\ninitialize-wiki.sh\napache2-foreground\n' > /tools/startup-container.sh && \
    chmod +x /build-tools/* /tools/*
ENV PATH="/tools:/build-tools:${PATH}"

CMD [ "startup-container.sh" ]
