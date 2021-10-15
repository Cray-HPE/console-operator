# Copyright 2020-2021 Hewlett Packard Enterprise Development LP
#
# Permission is hereby granted, free of charge, to any person obtaining a
# copy of this software and associated documentation files (the "Software"),
# to deal in the Software without restriction, including without limitation
# the rights to use, copy, modify, merge, publish, distribute, sublicense,
# and/or sell copies of the Software, and to permit persons to whom the
# Software is furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included
# in all copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.  IN NO EVENT SHALL
# THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
# OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
# ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
# OTHER DEALINGS IN THE SOFTWARE.
#
# (MIT License)

# Dockerfile for cray-console-operator service

# Build will be where we build the go binary
FROM arti.dev.cray.com/baseos-docker-master-local/golang:1.14-alpine3.12 as build
RUN set -eux \
    && apk add --upgrade --no-cache apk-tools \
    && apk update \
    && apk add build-base \
    && apk -U upgrade --no-cache

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
FROM artifactory.algol60.net/docker.io/alpine:3.13 as base

# Install conman application from package
RUN set -eux \
	&& apk add --upgrade --no-cache apk-tools \
    && apk update \
    && apk add --no-cache less openssh jq curl tar \
    && apk -U upgrade --no-cache

# Copy in the needed files
COPY --from=build /app/console_operator /app/
COPY scripts/console-ssh-keygen /app/console-ssh-keygen
COPY scripts/get-node /app/get-node

# Environment Variables -- Used by the HMS secure storage pkg
ENV VAULT_ADDR="http://cray-vault.vault:8200"
ENV VAULT_SKIP_VERIFY="true"

RUN echo 'alias ll="ls -l"' > /app/bashrc

# add a bunch of debug aliases
RUN echo 'alias health="curl -sk -X GET http://localhost:26777/console-operator/health"' >> /app/bashrc
RUN echo 'alias info="curl -sk -X GET http://localhost:26777/console-operator/info"' >> /app/bashrc
RUN echo 'alias suspend="curl -sk -X POST http://localhost:26777/console-operator/suspend"' >> /app/bashrc
RUN echo 'alias resume="curl -sk -X POST http://localhost:26777/console-operator/resume"' >> /app/bashrc
RUN echo 'alias clearData="curl -sk -X DELETE http://localhost:26777/console-operator/clearData"' >> /app/bashrc

# set to user nobody so this won't run as root
USER 65534:65534

ENTRYPOINT ["/app/console_operator"]
