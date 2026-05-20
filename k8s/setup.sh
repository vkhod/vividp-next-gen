#!/bin/bash
# Bootstrap the VividP Kubernetes environment in kind.
# Run this once after: kind create cluster --name vividp --config kind-config.yaml
set -e

echo "==> Building Docker images..."
docker build -f Dockerfile.ingestion      -t vividp/ingestion:dev      .
docker build -f Dockerfile.job-admin-api  -t vividp/job-admin-api:dev  .
docker build -f Dockerfile.admin-ui       -t vividp/admin-ui:dev       .

echo "==> Loading images into kind cluster..."
kind load docker-image vividp/ingestion:dev      --name vividp
kind load docker-image vividp/job-admin-api:dev  --name vividp
kind load docker-image vividp/admin-ui:dev       --name vividp

echo "==> Creating namespace..."
kubectl apply -f k8s/00-namespace.yaml

echo "==> Creating ConfigMap and Secret..."
kubectl apply -f k8s/01-configmap.yaml
kubectl apply -f k8s/02-secret.yaml

echo "==> Creating migration ConfigMap from SQL files..."
# --dry-run=client -o yaml | kubectl apply handles re-runs without error
kubectl create configmap db-migrations \
  --from-file=001_settings.sql=db/migrations/001_settings.sql \
  --from-file=002_jobs.sql=db/migrations/002_jobs.sql \
  -n vividp \
  --dry-run=client -o yaml | kubectl apply -f -

echo "==> Applying all manifests..."
kubectl apply -f k8s/ -R

echo ""
echo "Done! Watch pods come up with:"
echo "  kubectl -n vividp get pods -w"
echo ""
echo "Services:"
echo "  Admin UI:      http://localhost:30080"
echo "  Job Admin API: http://localhost:30081/api/admin/jobs"
echo "  MinIO console: http://localhost:30900  (user: vividp / vividp_dev)"
