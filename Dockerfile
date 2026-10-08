# drillbook with what runbook blocks and faults need: bash, kubectl, and
# ansible-core with an SSH client. Mount drillbook.yaml, drills/ and runbooks/.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/VivianSobers/drillbook/internal/cli.Version=${VERSION}" -o /drillbook ./cmd/drillbook

FROM alpine:3.22
ARG KUBECTL_VERSION=v1.36.4
RUN apk add --no-cache bash ca-certificates curl openssh-client python3 py3-pip \
 && pip install --no-cache-dir --break-system-packages ansible-core==2.21.5 \
 && curl -fsSLo /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" \
 && chmod +x /usr/local/bin/kubectl \
 && adduser -D -u 10001 drillbook
COPY --from=build /drillbook /usr/local/bin/drillbook
COPY ansible/collections /usr/share/drillbook/collections
ENV ANSIBLE_COLLECTIONS_PATH=/usr/share/drillbook/collections
USER drillbook
WORKDIR /work
ENTRYPOINT ["drillbook"]
