# Authentication

The Cloud Hypervisor API supports optional JWT Bearer token authentication.
When enabled, all requests to protected endpoints must include a valid
`Authorization` header.

## Enabling Authentication

Authentication is disabled by default.  Enable it via configuration:

```yaml
auth:
  enabled: true
  secret: "${JWT_SECRET}"   # HS256 signing key (required)
  issuer: "ch-api-issuer"    # optional
  audience: "ch-api"         # optional
  rbac_enabled: true         # default true when auth is enabled
```

Or via environment variables:

```bash
export CH_API_AUTH_ENABLED=true
export CH_API_AUTH_SECRET=super-secret-key
export CH_API_AUTH_ISSUER=ch-api-issuer
export CH_API_AUTH_AUDIENCE=ch-api
export CH_API_AUTH_RBAC_ENABLED=true
```

## Token Format

Tokens are standard [JWTs](https://tools.ietf.org/html/rfc7519) signed with
**HS256** (HMAC-SHA256).  The expected HTTP header is:

```
Authorization: Bearer <token>
```

### Required Claims

| Claim | Description |
|-------|-------------|
| `sub` | Subject identifier (user or service account ID).  Must be present and non-empty. |
| `exp` | Expiration time.  Tokens without `exp` are accepted but strongly discouraged. |

### Optional Claims

| Claim | Description |
|-------|-------------|
| `roles` | JSON array of role strings, e.g. `["admin","operator"]`.  Extracted into the request context for downstream authorization. |
| `iss` | Issuer.  Validated when `auth.issuer` is configured. |
| `aud` | Audience.  Validated when `auth.audience` is configured.  May be a single string or array of strings. |
| `nbf` | Not Before.  Standard JWT validation applies. |
| `iat` | Issued At.  Standard JWT validation applies. |

### Example Token Payload

```json
{
  "sub": "svc-account-42",
  "roles": ["admin", "operator"],
  "iss": "ch-api-issuer",
  "aud": "ch-api",
  "iat": 1715731200,
  "exp": 1715734800
}
```

## Validation Rules

1. **Header presence** — Requests without an `Authorization` header receive
   `401 Unauthorized`.
2. **Scheme** — The header must use the `Bearer` scheme.  `Basic`, `Digest`, etc.
   are rejected with `401 Unauthorized`.
3. **Signature** — The token must be signed with HS256 and the configured secret.
   Any algorithm mismatch or invalid signature results in `401 Unauthorized`.
4. **Expiry** — Tokens with an `exp` claim in the past are rejected.
5. **Subject** — The `sub` claim is mandatory.  Missing or empty `sub` causes
   `401 Unauthorized`.
6. **Issuer** — When `auth.issuer` is configured, the `iss` claim must match.
7. **Audience** — When `auth.audience` is configured, the `aud` claim must
   contain the expected value.

## Error Response

On any validation failure the API returns `401 Unauthorized` with an
[RFC 7807](https://tools.ietf.org/html/rfc7807) Problem Details body:

```json
{
  "type": "https://github.com/org/ch-api/errors/unauthorized",
  "title": "Unauthorized",
  "status": 401,
  "detail": "token expired",
  "instance": "/api/v1/vms"
}
```

## Context Values

After successful authentication the middleware injects two values into the
`request.Context()`:

| Key     | Type       | Accessor                           |
|---------|------------|------------------------------------|
| Subject | `string`   | `auth.SubjectFromContext(ctx)`     |
| Roles   | `[]string` | `auth.RolesFromContext(ctx)`       |

These values can be used by downstream handlers or middleware for fine-grained
authorization decisions.

## Role-Based Access Control (RBAC)

When `auth.rbac_enabled` is `true` (default) the API enforces a three-tier
role hierarchy.  Higher roles implicitly inherit all permissions of lower
roles.

| Role       | Level | Permissions |
|------------|-------|-------------|
| `viewer`   | 0     | Read-only: list VMs, get VM details, stream console. |
| `operator` | 1     | viewer + lifecycle operations (boot, pause, resume, shutdown, reboot) and resource changes (resize CPU, resize memory). |
| `admin`    | 2     | operator + create/delete VMs, attach/detach disks, create snapshots, add/remove/patch network interfaces. |

### Permission Table

| Method | Path Pattern | Minimum Role |
|--------|--------------|--------------|
| `GET`    | `/api/v1/vms` | viewer |
| `GET`    | `/api/v1/vms/{id}` | viewer |
| `GET`    | `/api/v1/vms/{id}/console` | viewer |
| `POST`   | `/api/v1/vms/{id}/boot` | operator |
| `POST`   | `/api/v1/vms/{id}/pause` | operator |
| `POST`   | `/api/v1/vms/{id}/resume` | operator |
| `POST`   | `/api/v1/vms/{id}/shutdown` | operator |
| `POST`   | `/api/v1/vms/{id}/reboot` | operator |
| `PATCH`  | `/api/v1/vms/{id}/cpu` | operator |
| `PATCH`  | `/api/v1/vms/{id}/memory` | operator |
| `POST`   | `/api/v1/vms` | admin |
| `DELETE` | `/api/v1/vms/{id}` | admin |
| `POST`   | `/api/v1/vms/{id}/disks` | admin |
| `DELETE` | `/api/v1/vms/{id}/disks/{disk_id}` | admin |
| `POST`   | `/api/v1/vms/{id}/disks/{disk_id}/snapshot` | admin |
| `POST`   | `/api/v1/vms/{id}/interfaces` | admin |
| `DELETE` | `/api/v1/vms/{id}/interfaces/{iface_id}` | admin |
| `PATCH`  | `/api/v1/vms/{id}/interfaces/{iface_id}` | admin |

### Authorization Denial

When a request is denied due to insufficient role level the API returns
`403 Forbidden` with an RFC 7807 Problem Details body:

```json
{
  "type": "https://github.com/org/ch-api/errors/forbidden",
  "title": "Forbidden",
  "status": 403,
  "detail": "insufficient permissions: requires admin role",
  "instance": "/api/v1/vms"
}
```

Every authorization denial is logged to the structured logger with the
subject, assigned roles, required role, method, and path.  The request is
also recorded by the audit middleware with status code `403`.

## Public Endpoints

The following endpoints are intentionally **public** and do not require
authentication:

* `GET /healthz` — Health check
* `GET /api/docs/*` — Swagger UI
* `GET /metrics` — Prometheus metrics (when enabled)

All other endpoints under `/api/v1` require a valid token when authentication
is enabled.  Endpoints not listed in the RBAC permission table (e.g.
`GET /api/v1/status`) require a valid token but do not enforce a specific
role.
