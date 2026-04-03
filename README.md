# sitespeed.io API

A Go REST API that wraps [sitespeed.io](https://www.sitespeed.io/) to run web performance analyses via HTTP. Results are stored in S3-compatible storage and can be retrieved on demand.

## Features

- Run sitespeed.io analyses via HTTP API
- Docker and Kubernetes runner backends
- S3-compatible result storage (AWS S3, MinIO, etc.)
- Web Vitals extraction (TTFB, LCP, FCP, CLS, transfer size)
- Screenshot capture
- Automatic cleanup of stale containers/pods and result files
- Optional bearer token authentication
- Optional OpenTelemetry tracing with trace-aware request logging

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/healthz` | Basic health check for container/orchestrator probes |
| `POST` | `/api/result/{id}` | Run a sitespeed.io analysis |
| `DELETE` | `/api/result/{id}` | Delete stored results |
| `GET` | `/result/{id}/{path...}` | Browse the full HTML report |
| `GET` | `/screenshot/{id}` | Get the page screenshot |

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

## Configuration

### General

| Variable | Description | Default |
|----------|-------------|---------|
| `AUTH_TOKEN` | Bearer token for `/api/*` endpoints | _(none, auth disabled)_ |
| `OTEL_SERVICE_NAME` | OpenTelemetry service name | `sitespeed-api` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP endpoint used for traces | _(none, disabled)_ |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | Trace-specific OTLP endpoint override | _(none, disabled)_ |
| `OTEL_EXPORTER_OTLP_HEADERS` | Headers for OTLP exporter (e.g. `key=value,key2=value2`) | _(none)_ |

When one of the OTLP endpoint variables is configured, incoming HTTP requests are traced and log lines emitted inside traced request flows include `trace_id` and `span_id` for correlation.

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
