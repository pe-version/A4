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

---

## Additional Hardening (post-review)

After an internal review of the A4-specific code against OWASP Top 10 and SWEBoK construction principles, two additional issues were identified and fixed.

### 4. Constant-Time Token Comparison (OWASP A07 — Auth Failures)

#### Before

Both `go-service/middleware/auth.go` and `go-alert-service/middleware/auth.go` compared the incoming Bearer token against the configured `API_TOKEN` using Go's `!=` operator:

```go
if token != validToken {
    // 401 Unauthorized
}
```

Go's string comparison short-circuits on the first mismatching byte. An attacker with access to response timing can recover the valid token byte-by-byte: each correctly-guessed prefix byte causes the comparison to run slightly longer before rejection. Under enough samples and low-noise network conditions, this reduces token recovery from a brute-force search of `O(256^n)` to `O(256·n)`.

#### After

Both middlewares now compute SHA-256 hashes of the valid token (once, at startup) and the incoming token (per request), then compare the 32-byte hashes with `crypto/subtle.ConstantTimeCompare`:

```go
validHash := sha256.Sum256([]byte(validToken))  // precomputed at startup
// ... per request ...
incomingHash := sha256.Sum256([]byte(parts[1]))
if subtle.ConstantTimeCompare(incomingHash[:], validHash[:]) != 1 {
    // 401 Unauthorized
}
```

The hash step is important: `ConstantTimeCompare` is only constant-time when inputs are the same length. If the code compared the raw tokens directly, a short wrong token would return faster than a long wrong token — the length comparison itself leaks information. Hashing first normalizes both sides to 32 bytes.

#### Why This Matters

Timing attacks on token comparison are a real, documented attack class (see OWASP's guidance on secret comparison). The per-request cost is a single SHA-256 hash — sub-microsecond — which is negligible compared to the database query that follows. The fix is small, has no behavioral impact on legitimate requests, and closes a category of attack that would otherwise be invisible in telemetry.

### 5. Pinned Base Image (OWASP A06 — Vulnerable and Outdated Components)

#### Before

Both Dockerfiles used an unpinned base image for the final stage:

```dockerfile
FROM alpine:latest
```

`:latest` is a mutable tag. A Docker build at time T and the same build at time T+1 can produce different images, with different installed packages, different CVE exposure, and potentially different runtime behavior. This is a reproducibility failure (SWEBoK configuration management) and a supply-chain risk (OWASP A06): a compromised or silently-updated base image propagates to every build without notice.

#### After

Both Dockerfiles now pin to an explicit Alpine release:

```dockerfile
# Minimal final image (pinned for reproducibility — OWASP A06)
FROM alpine:3.20
```

`alpine:3.20` is a specific release with a known package inventory. Upgrading is now an explicit decision — bump the tag, rebuild, run tests — rather than an implicit drift every time someone pulls.

#### Why This Matters

Pinned dependencies are a foundational software engineering practice (SWEBoK configuration management, supply chain security). Without pinning, every collaborator builds a subtly different image, and bugs or security changes introduced upstream appear in builds with no changelog entry. For a graded assignment this is mostly a reproducibility concern; for a production system, it's how supply-chain incidents like `xz-utils` propagate silently.

### Verification

Both services rebuilt and deployed with no functional regressions:

```
All 16 checks passed  (scripts/verify.sh)
```

The smoke test validates that:
- Valid Bearer tokens are accepted (200/201 responses)
- Invalid or missing tokens are rejected (401)
- The async pipeline (sensor update → RabbitMQ → alert) completes end-to-end

No change to external API contract; no change to test expectations; no change to performance at the scale measured (sub-microsecond SHA-256 vs. millisecond-scale DB queries).
