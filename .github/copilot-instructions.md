# Copilot instructions

- Keep changes focused and avoid unrelated edits.
- For Go changes, validate with `make build`, `make test-cover-check`, and `make lint`. Fix any issues before committing.
- Commit titles and pull request titles must follow the Conventional Commits pattern (for example `feat: ...`, `fix: ...`, `docs: ...`) because this repository uses release-please for releases.
- When you make changes to code, update unit tests and add new ones as needed. If you change the behavior of the app, update the README.md documentation as well.
- Always use the UI framework's components and styles for new UI elements including any text-based feedback to the user.