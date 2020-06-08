FROM ubuntu:16.04
COPY GeoLite2-City.mmdb /usr/share/
COPY upgrade-responder /usr/bin/
CMD ["upgrade-responder"]
ENV GEODB /usr/share/GeoLite2-City.mmdb
