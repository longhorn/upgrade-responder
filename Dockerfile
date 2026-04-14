# syntax=docker/dockerfile:1.22.0
FROM golang:1.22-alpine AS base

ARG TARGETARCH
ARG http_proxy
ARG https_proxy

ENV GOLANGCI_LINT_VERSION=v1.55.2

ENV ARCH=${TARGETARCH}

RUN apk -U add bash git gcc musl-dev docker vim less file curl wget ca-certificates

# Install golangci-lint
RUN curl -fsSL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh -o /tmp/install.sh \
    && chmod +x /tmp/install.sh \
    && /tmp/install.sh -b /usr/local/bin ${GOLANGCI_LINT_VERSION}

WORKDIR /go/src/github.com/longhorn/upgrade-responder
COPY . .

FROM base AS build
RUN ./scripts/build

FROM scratch AS build-artifacts
COPY --from=build /go/src/github.com/longhorn/upgrade-responder/bin/ /bin/

FROM base AS validate
RUN ./scripts/validate && touch /validate.done

FROM scratch AS validate-artifacts
COPY --from=validate /validate.done /validate.done
