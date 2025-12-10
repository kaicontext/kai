# Kailab Deployment Guide

## Prerequisites

- GCP project with GKE cluster
- Cloud Build API enabled
- Container Registry API enabled
- `gcloud` and `kubectl` configured

## Quick Deploy

### 1. Build and push images

```bash
# From kai repo root
gcloud builds submit --config=deploy/cloudbuild.yaml .
```

### 2. Update k8s manifest

Edit `deploy/k8s/kailab-deployment.yaml`:
- Replace `gcr.io/YOUR_PROJECT/` with your GCP project
- Replace `kailab.yourdomain.com` with your domain
- Adjust storage class if needed

### 3. Deploy to k8s

```bash
kubectl apply -f deploy/k8s/kailab-deployment.yaml
```

### 4. Verify deployment

```bash
kubectl get pods -n kailab
kubectl get svc -n kailab
kubectl get ingress -n kailab
```

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     Ingress                              │
│                kailab.yourdomain.com                     │
└─────────────────────┬───────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────┐
│              kailab-control (Control Plane)             │
│                    Port 8080                             │
│  • SvelteKit UI                                          │
│  • Tenant/repo management                                │
│  • Auth (future)                                         │
│  • SQLite: /data/kailab-control.db                      │
└─────────────────────┬───────────────────────────────────┘
                      │ Internal
                      ▼
┌─────────────────────────────────────────────────────────┐
│                kailab (Data Plane)                       │
│                    Port 7447                             │
│  • Graph storage (snapshots, changesets)                 │
│  • Object store (segments)                               │
│  • Ref management                                        │
│  • SQLite: /data/{tenant}/{repo}/kailab.db              │
└─────────────────────────────────────────────────────────┘
```

## CI Integration

Once deployed, update your `.gitlab-ci.yml`:

```yaml
variables:
  KAILAB_URL: "https://kailab.yourdomain.com/myorg/myrepo"

script:
  - kai init
  - kai remote add origin $KAILAB_URL
  - kai fetch origin
  - kai capture
  - kai ci plan --out plan.json
```

## Building Kai CLI for CI

To distribute the kai CLI binary for CI runners:

```bash
# Build for Linux (CI runners)
cd kai-cli
GOOS=linux GOARCH=amd64 go build -o kai-linux-amd64 ./cmd/kai

# Upload to GCS bucket (or your preferred location)
gsutil cp kai-linux-amd64 gs://your-bucket/kai/kai-linux-amd64
gsutil acl ch -u AllUsers:R gs://your-bucket/kai/kai-linux-amd64
```

Then in CI:
```yaml
before_script:
  - curl -sL https://storage.googleapis.com/your-bucket/kai/kai-linux-amd64 -o /usr/local/bin/kai
  - chmod +x /usr/local/bin/kai
```

## Scaling Considerations

Current setup uses SQLite (single-replica). For multi-replica:

1. **PostgreSQL migration** - Abstract storage layer to support PostgreSQL
2. **Redis for caching** - Session state, hot data
3. **Object storage** - Move segment blobs to GCS

These are future enhancements - SQLite with PersistentVolume works well for moderate scale.
