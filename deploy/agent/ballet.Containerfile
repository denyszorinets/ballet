# syntax=docker/dockerfile:1
# The agent image for Ballet's own repository (dogfooding): the agent
# image plus the toolchains its Makefile needs, installed where the session
# user "ballet" finds them.
#   make images
#   docker build -f deploy/agent/ballet.Containerfile -t ballet-dogfood deploy/agent
FROM ballet-agent
ARG GO_VERSION=1.27.1
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -xz -C /usr/local
ENV PATH="/usr/local/go/bin:${PATH}"
RUN curl -fsSL https://bun.sh/install | BUN_INSTALL=/usr/local bash && \
    curl -LsSf https://astral.sh/uv/install.sh | env UV_INSTALL_DIR=/usr/local/bin UV_NO_MODIFY_PATH=1 sh
