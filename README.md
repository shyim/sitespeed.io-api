# sitespeed.io API

A Go REST API that wraps [sitespeed.io](https://www.sitespeed.io/) to run web performance analyses via HTTP. Results are stored in S3-compatible storage and can be retrieved on demand.

## Features

- Run sitespeed.io analyses via HTTP API
- Docker and Kubernetes runner backends
- S3-compatible result storage (AWS S3, MinIO, etc.)
- Signed result links: reports stay shareable as plain URLs, protected by a static secret
- Web Vitals extraction (TTFB, LCP, FCP, CLS, transfer size)
- Screenshot capture
- Automatic cleanup of stale containers/pods and result files
- Optional bearer token authentication
- JSON structured logging for Datadog-friendly log ingestion
- Optional OpenTelemetry tracing with trace-aware request logging

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/healthz` | Basic health check for container/orchestrator probes |
| `POST` | `/api/result/{id}` | Run a sitespeed.io analysis |
| `DELETE` | `/api/result/{id}` | Delete stored results |
| `GET` | `/result/{id}/{path...}` | Browse the full HTML report (signed link required if `RESULT_AUTH_SECRET` is set) |
| `GET` | `/screenshot/{id}` | Get the page screenshot (signed link required if `RESULT_AUTH_SECRET` is set) |

### GET `/healthz`

Returns `200 OK` when the API process is up.

```json
{
  "status": "ok"
}
```

### POST `/api/result/{id}`

```json
{
  "urls": ["https://example.com"],
  "headers": {
    "Authorization": "Bearer token"
  }
}
```

- `urls` (required): 1-5 URLs to analyze
- `headers` (optional): Custom request headers passed to the browser

Response:

```json
{
  "ttfb": 123.45,
  "fullyLoaded": 2567.89,
  "largestContentfulPaint": 1200.5,
  "firstContentfulPaint": 800.3,
  "cumulativeLayoutShift": 0.05,
  "transferSize": 524288
}
```

## Signed result links

Without `RESULT_AUTH_SECRET`, `/result/...` and `/screenshot/...` are public to
anyone who knows the analysis id. Set `RESULT_AUTH_SECRET` and both endpoints
require a signature, so links can still be shared in Slack, a PR, or a browser
bookmark while staying unreadable to anyone without the secret.

The scheme is the static-secret flavour of an AWS presigned URL: the signature is
an HMAC-SHA256 over the analysis id, so it can be computed offline by any holder
of the secret — this API keeps no state and needs no signing endpoint.

- `sig` — required, `base64url(HMAC-SHA256(secret, "<id>\n<expires>"))`, unpadded
- `expires` — optional unix timestamp; omit for a link that never expires

```bash
ID=my-analysis
SECRET=your-signing-secret
EXPIRES=$(($(date +%s) + 86400))                       # valid for 24h

SIG=$(printf '%s\n%s' "$ID" "$EXPIRES" \
  | openssl dgst -sha256 -hmac "$SECRET" -binary \
  | openssl base64 -A | tr '+/' '-_' | tr -d '=')

echo "https://api.example.com/result/$ID/index.html?sig=$SIG&expires=$EXPIRES"
```

The signature covers the **analysis id, not the file path**, so a single link
opens the whole report. The API answers a valid signature with a short-lived
`HttpOnly` cookie (`sitespeed_result_auth`) scoped to that id, which is what lets
the report's relative assets — pages, CSS, JS, images — load, since browsers do
not propagate query parameters to sub-resources. Signing each file path
individually would return 401 as soon as you clicked a link inside the report.

**The same signature authorises the screenshot.** Because it is keyed on the id
alone, the value you already computed works on both endpoints:

```bash
# Report:  https://api.example.com/result/$ID/index.html?sig=$SIG&expires=$EXPIRES
# Picture: https://api.example.com/screenshot/$ID?sig=$SIG&expires=$EXPIRES
```

An `<img>` tag in your own UI can therefore point straight at the API:

```html
<img src="https://api.example.com/screenshot/my-analysis?sig=...&expires=...">
```

Note that a browser will not attach the query string to a stylesheet or script
loaded from a different origin, so embed pictures via a signed URL as shown, and
keep same-origin CSS/JS behind the report page itself.

Consequences worth knowing:

- The grant is bound to one analysis id; a link to one report never unlocks another.
- Rotating `RESULT_AUTH_SECRET` immediately invalidates all existing links and cookies.
- Cookies are `SameSite=Lax`, so a report opened in a private window does not
  share its grant with your normal browser profile.
- The `Authorization: Bearer <AUTH_TOKEN>` header is also accepted on these
  endpoints, which keeps scripted/API access working without signing anything.
- Set `RESULT_AUTH_TTL` to make links and cookies expire automatically; leave it
  unset and links stay valid indefinitely (cookies still default to 1 hour).
- This is authentication, not S3 pre-signed URLs: it guards this API, not direct
  access to the underlying bucket. Keep the bucket private.

## Configuration

### General

| Variable | Description | Default |
|----------|-------------|---------|
| `AUTH_TOKEN` | Bearer token for `/api/*` endpoints, also accepted on result endpoints | _(none, auth disabled)_ |
| `RESULT_AUTH_SECRET` | Static secret for signing `/result/*` and `/screenshot/*` links | _(none, result auth disabled)_ |
| `RESULT_AUTH_TTL` | Max lifetime for signed result links and their cookie, e.g. `24h` | _(none, links never expire)_ |
| `OTEL_SERVICE_NAME` | OpenTelemetry service name | `sitespeed-api` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP endpoint used for traces | _(none, disabled)_ |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | Trace-specific OTLP endpoint override | _(none, disabled)_ |
| `OTEL_EXPORTER_OTLP_HEADERS` | Headers for OTLP exporter (e.g. `key=value,key2=value2`) | _(none)_ |

Logs are emitted as JSON to `stderr` with Datadog-friendly top-level fields such as `timestamp`, `status`, `message`, and `service`. When one of the OTLP endpoint variables is configured, incoming HTTP requests are traced and log lines emitted inside traced request flows also include `trace_id` and `span_id` for correlation.

### Runner

| Variable | Description | Default |
|----------|-------------|---------|
| `RUNNER_TYPE` | `kubernetes` or `docker` | `docker` |
| `SITESPEED_IMAGE` | sitespeed.io container image | `sitespeedio/sitespeed.io:latest` |
| `RESULT_BASE_DIR` | Local directory for results | `/tmp/sitespeed-results` |
| `ANALYSIS_TIMEOUT` | Analysis timeout (also accepts deprecated `DOCKER_TIMEOUT`) | `300s` |
| `MAX_CONCURRENT_ANALYSES` | Max parallel analyses | `5` |

### Kubernetes-specific

| Variable | Description | Default |
|----------|-------------|---------|
| `KUBECONFIG` | Path to kubeconfig | `~/.kube/config` |
| `K8S_NAMESPACE` | Namespace for pods | `default` |
| `K8S_NODE_SELECTOR` | Node selector for analysis pods, as comma-separated `key=value` pairs (e.g. `disktype=ssd,pool=ci`) | _(none)_ |

### S3 Storage

| Variable | Description | Default |
|----------|-------------|---------|
| `S3_SERVICE_URL` | S3 endpoint URL | _(required)_ |
| `S3_ACCESS_KEY` | Access key | _(required)_ |
| `S3_SECRET_KEY` | Secret key | _(required)_ |
| `S3_BUCKET_NAME` | Bucket name | `sitespeed-results` |
| `S3_DISABLE_PAYLOAD_SIGNING` | Disable payload signing | `true` |

## Running locally

```bash
docker compose up
```

This starts the API on port 8080 with MinIO as the S3 backend. The Docker socket is mounted so the API can spawn sitespeed.io containers.

## Deployment

### Docker

```bash
docker build -t sitespeed-api .
docker run -p 8080:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e S3_SERVICE_URL=https://s3.example.com \
  -e S3_ACCESS_KEY=... \
  -e S3_SECRET_KEY=... \
  sitespeed-api
```

### Kubernetes

RBAC and deployment manifests are provided in `deploy/kubernetes/`. The API needs permissions to create, delete, and exec into pods.

```bash
kubectl apply -f deploy/kubernetes/
```
