# Decisions

Running log of implementation decisions. When the spec is ambiguous, choose the
simplest option that keeps the zero-setup demo working and record it here.

| # | Date | Decision | Rationale |
| --- | --- | --- | --- |
| D1 | 2026-11-09 | SQLite via `modernc.org/sqlite` (pure Go, no CGO) instead of the PostgreSQL named in the README architecture table. In-process `jobs` table + ticker instead of a Postgres queue. | Judges must run the project with only Go installed; no external services allowed. README's architecture section is updated in M6. |
| D2 | 2026-11-09 | Demo mode is the default and runs fully offline: fixture feeds, replayed LLM responses, `.eml` files in `./outbox`, fake payment provider. | No API keys or accounts may be required to evaluate the project. |
| D3 | 2026-11-09 | Replay fixtures are keyed by a stable hash of (model, prompt template name, normalized input). Missing key = hard failure naming the key. | Makes fixtures deterministic and regenerable via `kloxus record`. |
| D4 | 2026-11-09 | The Anthropic client is built in milestone 2, before the `record` command is polished in milestone 4. | Replay fixtures can only be produced by real API calls; M2 cannot finish without them. |
| D5 | 2026-11-09 | Model IDs are configurable env vars. The spec's `claude-haiku-5-5` / `claude-sonnet-5-5` are placeholders to be verified against the API at integration time. | Model naming changes; never hard-code. |
| D6 | 2026-11-09 | All UI assets (CSS, htmx) are embedded with `go:embed`. No CDN links. | The UI must work offline. |
| D7 | 2026-11-09 | No hosted deployment for the MVP. The single-binary, interface-swapped design is chosen so a hosted SaaS version remains possible later. | Hackathon constraint; SaaS is a post-pilot decision. |
| D8 | 2026-11-09 | Reader payments go through the creator's own AbacatePay account; Kloxus stores a restricted, AES-256-GCM-encrypted key and never holds money. | Editorial principle: readers pay the creator, not us. |
| D9 | 2026-11-09 | Fixture articles are fictional and original, written for this repo, with fictional outlets. No real article text is committed. | License safety and a clear conscience for the curation-is-not-reproduction principle. |
