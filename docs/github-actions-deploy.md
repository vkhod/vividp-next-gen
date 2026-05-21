# GitHub Actions Deployment

This repository builds Docker images, pushes them to GitHub Container Registry, then deploys the production Compose stack to this server over SSH.

## Server Setup

The deploy target for this server is:

```bash
/opt/vividp-next-gen
```

The local production environment file is:

```bash
/opt/vividp-next-gen/.env
```

That file is intentionally ignored by Git. Keep real secrets there on the server only.

## Generated SSH Key

A dedicated deploy keypair has been generated on this server:

```bash
/root/.ssh/vividp_next_gen_github_actions
/root/.ssh/vividp_next_gen_github_actions.pub
```

The public key has already been added to:

```bash
/root/.ssh/authorized_keys
```

To view the private key for GitHub Actions:

```bash
cat /root/.ssh/vividp_next_gen_github_actions
```

Add that full private key, including the `BEGIN` and `END` lines, as the `SERVER_SSH_KEY` repository secret.

## Required GitHub Secrets

In GitHub, open:

```text
Settings -> Secrets and variables -> Actions -> New repository secret
```

Create these secrets:

| Secret | Value |
|---|---|
| `SERVER_HOST` | `77.42.123.217` |
| `SERVER_SSH_KEY` | Contents of `/root/.ssh/vividp_next_gen_github_actions` |

## Production Environment

Before the first deploy, edit:

```bash
/opt/vividp-next-gen/.env
```

Set a real production value for:

```bash
ANTHROPIC_API_KEY
```

The generated `POSTGRES_PASSWORD` and `MINIO_ROOT_PASSWORD` values can stay as-is unless you want to rotate them.

## Deploy Flow

On every push to `master`, GitHub Actions will:

1. Build and push the service images to GHCR.
2. Copy `docker-compose.prod.yml` to `/opt/vividp-next-gen`.
3. Run:

```bash
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d --remove-orphans
docker image prune -f
```

## First Deploy Check

After the first successful workflow run, verify the stack:

```bash
cd /opt/vividp-next-gen
docker compose -f docker-compose.prod.yml ps
docker compose -f docker-compose.prod.yml logs --tail=100 nginx
```
