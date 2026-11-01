# CampusTrace preparation service

The application's interview-preparation executor. LangGraph routes model, retrieval,
validation and clarification nodes; Go owns scoped materials, model admission and
confirmed business writes. This service does not expose a separate user interface.

See [deployment, recovery and evaluation](../../docs/preparation-langgraph.md).
Install with `make preparation-install`; run unit tests with `make preparation-test`.
`requirements.lock` pins transitive dependencies and hashes. Use one process per
SQLite checkpoint volume. Model credentials are held in Go, not in graph state.
