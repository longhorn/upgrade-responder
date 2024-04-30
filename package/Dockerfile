# syntax=docker/dockerfile:1.7.0

FROM ubuntu:20.04

ARG TARGETPLATFORM
RUN if [ "$TARGETPLATFORM" != "linux/amd64" ] && [ "$TARGETPLATFORM" != "linux/arm64" ]; then \
    echo "Error: Unsupported TARGETPLATFORM: $TARGETPLATFORM" && \
    exit 1; \
    fi

ENV ARCH ${TARGETPLATFORM#linux/}

COPY GeoLite2-City.mmdb /usr/share/
COPY upgrade-responder-${ARCH} /usr/bin/upgrade-responder
CMD ["upgrade-responder"]
ENV GEODB /usr/share/GeoLite2-City.mmdb
