# A3 Evidence (Reference)

This directory holds screenshots captured during A3 development. They are preserved here as historical reference and are not part of the A4 grading deliverables.

| File | Description |
|------|-------------|
| `a3_services_running.png` | Docker Desktop showing all four microservices (Python + Go sensor, Python + Go alert) + RabbitMQ running simultaneously under A3. |
| `a3_go_tests_running.png` | Terminal output from the Go service test suite (A3-era, SQLite-backed). |
| `a3_python_tests_running.png` | Terminal output from the Python service test suite (A3-era, SQLite-backed). |

## Where to find A4 evidence

A4-specific evidence lives in the `results/` directory:

- `results/scaling/` — horizontal scaling experiment (1 vs 3 replicas)
- `results/chaos/` — failure and chaos testing (kill replica, stop DB, disconnect queue)
- `results/security/` — network isolation, secrets management, secure headers
- `results/observability/` — distributed tracing and debugging story
