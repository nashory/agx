ARG GO_VERSION=1.26.3

FROM golang:${GO_VERSION}-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /out/agx ./cmd/agx

FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV HOME=/home/agx
ENV AGX_CONFIG_DIR=/home/agx/.config/agx

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        git \
        gosu \
        less \
        openssh-client \
        ripgrep \
        sqlite3 \
        tmux \
        zsh \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /home/agx/.config/agx /workspace \
    && chmod 0755 /home/agx /home/agx/.config /home/agx/.config/agx /workspace

COPY --from=builder /out/agx /usr/local/bin/agx
COPY docker/entrypoint.sh /usr/local/bin/agx-docker-entrypoint

RUN chmod +x /usr/local/bin/agx /usr/local/bin/agx-docker-entrypoint

WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/agx-docker-entrypoint"]
CMD ["bash"]
