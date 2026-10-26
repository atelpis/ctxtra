# Agent contract tests in a Linux container. Build from the repository root:
#
#   docker build -f e2e/Dockerfile -t ctxtra-e2e .
#   docker run --rm --network none ctxtra-e2e
#
# With --build-arg AGENTS=latest it tests the latest agent releases.
#
# --network none: the agents can reach only the mock on 127.0.0.1. Everything
# is downloaded while building the image.
FROM golang:1.27-trixie@sha256:fbb7ae40b62ff8afe31662df570d39b4f5052ac32b90dae8e60d7657a20a74f4 AS go

FROM node:26-trixie-slim@sha256:36793bcbb160a6a16cede1e62b93f84e26b78a972a8a4c0b879c1c12f22761ec
COPY --from=go /usr/local/go /usr/local/go
# Python 3.13 for Hermes, tmux for the TUI tests.
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates curl python3 python3-venv tmux \
	&& rm -rf /var/lib/apt/lists/*

# Agents refuse some modes as root, and the tests should not need it.
RUN useradd --create-home e2e
USER e2e
ENV PATH=/usr/local/go/bin:$PATH GOFLAGS=-mod=readonly GOTOOLCHAIN=local
WORKDIR /src

# AGENTS=latest installs the latest agent releases instead of the pinned ones.
ARG AGENTS=pinned
COPY --chown=e2e e2e/agents/package.json e2e/agents/package-lock.json e2e/agents/install.sh \
	e2e/agents/install-muse.mjs e2e/agents/muse.json e2e/agents/antigravity.lock e2e/agents/hermes.ref \
	e2e/agents/requirements.txt e2e/agents/
RUN e2e/agents/install.sh "$AGENTS"

COPY --chown=e2e go.mod go.sum ./
RUN go mod download
COPY --chown=e2e . .
# Compile everything now, so the run needs no network.
RUN go build ./... && cd e2e && go test -count=1 -run '^$' ./...

# Codex's sandbox needs user namespaces, which an unprivileged container
# doesn't allow; see driver/codex.go.
ENV GOPROXY=off CTXTRA_E2E_REQUIRE=1 CTXTRA_E2E_CODEX_SANDBOX=danger-full-access
WORKDIR /src/e2e
CMD ["go", "test", "-count=1", "./..."]
