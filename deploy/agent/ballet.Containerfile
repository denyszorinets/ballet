# syntax=docker/dockerfile:1
# The agent image for Ballet's own repository (dogfooding): the general
# agent image plus the toolchains its Makefile needs.
#   docker build -f deploy/agent/Containerfile -t ballet-agent deploy/agent
#   docker build -f deploy/agent/ballet.Containerfile -t ballet-dogfood deploy/agent
FROM ballet-agent
ARG GO_VERSION=1.27.1
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -xz -C /usr/local
ENV PATH="/usr/local/go/bin:/root/go/bin:/root/.bun/bin:/root/.local/bin:${PATH}"
RUN curl -fsSL https://bun.sh/install | bash && \
    curl -LsSf https://astral.sh/uv/install.sh | sh
