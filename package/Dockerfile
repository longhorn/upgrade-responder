# syntax=docker/dockerfile:1.7.0

FROM ubuntu:20.04

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates wget curl \
 && update-ca-certificates \
 && rm -rf /var/lib/apt/lists/*
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

ARG TARGETPLATFORM
RUN if [ "$TARGETPLATFORM" != "linux/amd64" ] && [ "$TARGETPLATFORM" != "linux/arm64" ]; then \
    echo "Error: Unsupported TARGETPLATFORM: $TARGETPLATFORM" && \
    exit 1; \
    fi
ENV ARCH ${TARGETPLATFORM#linux/}

COPY GeoLite2-City.mmdb /usr/share/
COPY upgrade-responder-${ARCH} /usr/bin/upgrade-responder

ENV GEODB /usr/share/GeoLite2-City.mmdb
CMD ["upgrade-responder"]

