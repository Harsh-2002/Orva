# docs/

Human-maintained reference documentation. Keep these in sync when changing API shapes, config keys, runtime behavior, or operational procedures.

| File | Contents |
|---|---|
| `API.md` | Full REST API reference — every endpoint, request/response shapes, error codes |
| `ARCHITECTURE.md` | System design, component diagram, request lifecycle |
| `CAPACITY.md` | Sizing guide, pool tuning, resource limits per runtime |
| `CONFIG.md` | All configuration keys, environment variables, and defaults |
| `CONTRIBUTING.md` | Dev environment setup, PR process, code style conventions |
| `DEPLOYMENT.md` | Docker, bare-metal, reverse proxy setup, TLS termination |
| `ERRORS.md` | Error slug catalog — `SLUG → HTTP status → human meaning` |
| `OPERATIONS.md` | Day-2 ops: backup/restore, VACUUM, log rotation, upgrades |
| `RUNTIMES.md` | Per-runtime handler contract, streaming (generators/async iterables), TypeScript |
| `SECURITY.md` | Threat model, nsjail sandbox isolation, network firewall (nftables) |

## Update Triggers

- New REST endpoint → update `API.md`
- New config key → update `CONFIG.md`
- New error slug → update `ERRORS.md`
- Runtime behavior change → update `RUNTIMES.md`
- Backup/restore or vacuum changes → update `OPERATIONS.md`
- Security boundary change → update `SECURITY.md`
