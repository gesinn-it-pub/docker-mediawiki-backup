# MW_VERSION selects the MediaWiki release (1.39, 1.43); the image already contains docker-mediawiki-tools.
ARG MW_VERSION=1.43
FROM gesinn/mediawiki-ci:${MW_VERSION}

# Install MediaWiki on first start (against the database given by MYSQL_HOST), then run Apache.
RUN rm -rf LocalSettings.php /var/www/data && \
    printf '#!/bin/bash\ninitialize-wiki.sh\napache2-foreground\n' > /tools/startup-container.sh && \
    chmod +x /tools/startup-container.sh

CMD [ "startup-container.sh" ]
