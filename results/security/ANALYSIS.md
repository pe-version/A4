# Security Enhancements �� A4

## 1. Network Isolation (Restricted Ingress Rules)

### Before

All containers shared Docker Compose's default flat network. Every service could reach every other service and every database — the Python services could connect to `sensor-db` (Postgres), the Go sensor service could query `alert-db`, and any compromised container had full lateral movement capability.

### After

Six isolated Docker networks enforce principle of least privilege:

| Network | Members | Purpose |
|---------|---------|---------|
| `sensor-db` | go-service, sensor-db | Sensor replicas ↔ their Postgres only |
| `alert-db` | go-alert-service, alert-db | Alert replicas ↔ their Postgres only |
| `rabbitmq` | go-service, go-alert-service, python-service, python-alert-service, rabbitmq | Message bus access |
| `go-sensor-internal` | go-service replicas, go-sensor-lb, go-alert-service | LB ↔ sensor replicas |
| `go-alert-internal` | go-alert-service replicas, go-alert-lb, go-sensor-lb | LB ↔ alert replicas |
| `python-frontend` | python-service, python-alert-service | Python inter-service communication |

### Verification

```
Python service trying to reach sensor-db (should fail):
PASS: python-service CANNOT reach sensor-db (gaierror)

Go service trying to reach alert-db (should fail):
PASS: go-service CANNOT reach alert-db (bad address)
```

### Why This Matters

A compromised Go sensor service replica can only reach its own database (`sensor-db`), the message bus, and its load balancer. It cannot reach the alert database, the Python services, or any other infrastructure. This limits blast radius in a security incident and prevents accidental cross-service database access.

---

## 2. Secrets Management

### Before

Credentials were hardcoded as defaults throughout the codebase:

- `docker-compose.yml`: `${DB_PASSWORD:-iot_secret}`, `${RABBITMQ_PASS:-iot_secret}`
- `go-service/config/config.go`: `"postgres://iot_user:iot_secret@sensor-db:5432/sensors?sslmode=disable"`
- `go-alert-service/config/config.go`: `"amqp://iot_service:iot_secret@rabbitmq:5672/"`

Anyone with access to the repository had all production credentials.

### After

- **`.env` file** (gitignored) holds all secrets: `API_TOKEN`, `DB_USER`, `DB_PASSWORD`, `RABBITMQ_USER`, `RABBITMQ_PASS`
- **`.env.example`** committed as a template with `changeme` placeholders
- **Go config files** require `DATABASE_DSN`, `RABBITMQ_URL`, and `API_TOKEN` as mandatory environment variables — the service refuses to start without them (no fallback defaults)
- **`docker-compose.yml`** uses `${VAR:?error}` syntax for required variables — Compose fails at startup if `.env` is missing

### Why This Matters

Credentials never appear in version-controlled files. A new developer clones the repo, copies `.env.example` to `.env`, fills in credentials, and runs. Secrets rotate by editing one file, not by grepping the codebase. This is the minimum viable secrets management for a container-based system; production would use Docker secrets, HashiCorp Vault, or cloud KMS.

---

## 3. Secure Response Headers

### Before

Nginx proxied responses with no security headers. The `Server` header exposed the exact Nginx version (`nginx/1.29.8`), aiding fingerprinting.

### After

Both Nginx load balancers add the following headers to every response:

| Header | Value | Purpose |
|--------|-------|---------|
| `X-Content-Type-Options` | `nosniff` | Prevents MIME-type sniffing |
| `X-Frame-Options` | `DENY` | Prevents clickjacking via iframe embedding |
| `X-XSS-Protection` | `1; mode=block` | Enables browser XSS filter |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | Limits referrer information leakage |
| `Content-Security-Policy` | `default-src 'none'; frame-ancestors 'none'` | Restricts resource loading |
| `server_tokens` | `off` | Hides Nginx version from `Server` header |

### Verification

```
Server: nginx
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
X-XSS-Protection: 1; mode=block
Referrer-Policy: strict-origin-when-cross-origin
Content-Security-Policy: default-src 'none'; frame-ancestors 'none'
```

### Why This Matters

These headers are defense-in-depth measures applied at the reverse proxy layer. They protect against common web attack vectors (XSS, clickjacking, MIME confusion) without any application code changes. The CSP is strict (`default-src 'none'`) because this is a JSON API with no browser-rendered content — there is no legitimate reason for a browser to load scripts, images, or styles from these endpoints. Hiding the server version removes a free fingerprinting signal for attackers.
