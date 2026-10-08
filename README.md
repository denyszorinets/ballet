# Ballet

Ballet orchestrates AI software development. A human plans work with a
planner agent; the plan becomes tickets; each ticket runs through a
configurable pipeline of independent coding-agent sessions (implement,
review, verify, integrate) in fresh devcontainers — while a separate
knowledge service keeps documentation, decisions and debt from decaying.

*The human sleeps — Ballet works.*

## Documentation

Website and documentation: https://denyszorinets.github.io/ballet/
(published from `develop`). The documentation is a Sphinx site under
[`docs/`](docs/index.rst):

- [Quickstart](docs/getting-started/quickstart.rst) — Ballet, a demo project and a first ticket done by agents, without an API key
- [User guide](docs/user-guide/index.rst)
- [Vision](docs/concepts/vision.rst)
- [Architecture overview](docs/architecture/overview.rst)
- [Architecture decisions](docs/architecture/decisions/index.rst)

```bash
make docs         # build into docs/_build/html
make docs-serve   # live preview on http://127.0.0.1:8000
make docs-check   # strict build + link check
make site         # landing page + docs, as published, into _site/
```

## Run it

```bash
make run          # build the web UI into Core, build all services, run everything
```

Open http://localhost:8080 — no sign-in needed: locally you are the single
user. `BALLET_AUTH=oidc make run` enables multi-user sign-in through the
development Keycloak (`alice` / `alice`; needs Java 21+). Needs Go and
Bun; see
[Run Ballet locally](docs/how-to/run-locally.rst). To install it with
containers on one host, see
[Install Ballet on one host](docs/how-to/install-single-host.rst).

## Development

Go (backend services) and Svelte (web UI). Run `make help` for all
development targets. Work is tracked in GitHub issues and follows
GitFlow: feature branches from `develop`, pull requests into `develop`,
releases on `main`.
