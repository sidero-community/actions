# Contributing

Thanks for your interest.

- Bugs and change requests go through [GitHub issues](https://github.com/sidero-community/actions/issues).
- Building is handled by `make`; see `make help` for the targets.
- Run `make test` and `make lint` before opening a pull request.
- Each action is a standalone `main` package under its own directory with a `Dockerfile` and a
  `README.md` documenting its environment variables. Shared code lives under `pkg/`.
- Keep commit messages in the conventional form used in the history (`feat:`, `fix:`, `docs:`, `chore:`).
