FROM ubuntu:20.04

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates wget curl \
 && update-ca-certificates \
 && rm -rf /var/lib/apt/lists/*
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

COPY GeoLite2-City.mmdb /usr/share/
COPY upgrade-responder /usr/bin/
ENV GEODB /usr/share/GeoLite2-City.mmdb

CMD ["upgrade-responder"]

