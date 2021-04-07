# Dockerfile for cray-console-operator service
# Copyright 2021 Hewlett Packard Enterprise Development LP

# Build will be where we build the go binary
FROM arti.dev.cray.com/baseos-docker-master-local/golang:1.14-alpine3.12 AS build
RUN set -eux \
    && apk update \
    && apk add --no-cache build-base

# Configure go env - installed as package but not quite configured
ENV GOPATH=/usr/local/golib
RUN export GOPATH=$GOPATH

# Copy in all the necessary files
COPY src/console_op $GOPATH/src/console_op
COPY vendor/ $GOPATH/src

# Build configure_conman
RUN set -ex && go build -v -i -o /app/console_operator $GOPATH/src/console_op

### Final Stage ###
# Start with a fresh image so build tools are not included
FROM arti.dev.cray.com/baseos-docker-master-local/alpine:3.12.4

# Install conman application from package
RUN set -eux \
    && apk update \
    && apk add --no-cache less openssh jq curl tar

# Copy in the needed files
COPY --from=build /app/console_operator /app/

# Environment Variables -- Used by the HMS secure storage pkg
ENV VAULT_ADDR="http://cray-vault.vault:8200"
ENV VAULT_SKIP_VERIFY="true"

RUN echo 'alias ll="ls -l"' > ~/.bashrc
RUN echo 'alias vi="vim"' >> ~/.bashrc

ENTRYPOINT ["/app/console_operator", "-debug"]
