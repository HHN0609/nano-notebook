# Existing EC2 Production Recovery Design

## Goal

Restore the existing Nano Notebook production deployment on the stopped
`nano-notebook` EC2 instance, deploy the current `main` revision, and leave a
repeatable GitHub Actions deployment path.

## Current State

- Production is a single ARM64 EC2 instance running Docker Compose.
- The instance is stopped, so the latest GitHub Actions deployments time out
  while connecting to SSH.
- The latest workflow successfully builds and pushes its declared images before
  reaching the failed SSH step.
- The production Compose topology has evolved beyond the original deployment
  workflow. In particular, it now includes the Agent Trace Processor backed by
  Kafka and ClickHouse.
- Existing EC2 disks and Docker volumes may contain product data and must be
  preserved.

## Selected Approach

Recover the existing instance instead of rebuilding it:

1. Repair the repository-owned deployment workflow so it covers every
   repository-built image referenced by production Compose.
2. Validate the production Compose model before changing the server.
3. Start the existing EC2 instance and wait for AWS instance and system checks.
4. Resolve its current public address and update the GitHub `EC2_HOST` secret.
5. Ensure the deployment writes the same public address to the server's
   gitignored Compose environment as `NANO_PUBLIC_HOST` without exposing the
   rest of that file.
6. Build and push images tagged with the exact Git commit SHA, then deploy that
   tag through GitHub Actions.
7. Run required application and observability migrations before accepting the
   deployment.
8. Verify container health and the public web/API path.

## Workflow Changes

The deployment workflow will:

- ensure all application ECR repositories exist, including the Agent Trace
  Processor repository;
- build and push all repository-owned images referenced by
  `infra/compose/compose.prod.yaml`;
- use the immutable commit SHA as the deployed image tag;
- pull images before replacing running containers;
- update only `NANO_PUBLIC_HOST` in the existing server-side `.env` file;
- run database migrations through a production application image with the same
  database configuration as the Compose stack;
- start or reconcile the stack with `docker compose up -d`;
- report Compose status and fail if required services do not become healthy.

The workflow must not print secrets or the contents of the production `.env`
file.

## Data Safety

- Do not run `docker compose down -v`.
- Do not remove named volumes, EBS volumes, database files, MinIO objects,
  Qdrant data, ClickHouse data, or Kafka data.
- Do not rebuild or replace the EC2 instance.
- Image pruning may remove unused images only after the new deployment passes
  health checks.
- If migration or startup fails, retain the data volumes and inspect the first
  failing service. Do not reset persistent state to make the deployment pass.

## Verification

Acceptance requires all of the following:

1. The workflow revision is present on `origin/main` and its Deploy run succeeds.
2. EC2 state and AWS instance/system checks are healthy.
3. `docker compose ps` shows all required long-running services running and all
   defined health checks healthy.
4. The public root page responds successfully through nginx.
5. The public API health/session boundary responds without a gateway error.
6. PostgreSQL, MinIO, Qdrant, Kafka, ClickHouse, Redis, and the application
   services remain available after reconciliation.
7. The deployed server checkout and image tag correspond to the accepted Git
   commit SHA.

## Failure and Rollback

- If the instance cannot pass AWS checks, stop before changing application
  state and diagnose the EC2/network layer.
- If SSH remains unreachable, verify the current public address and security
  group before changing application files.
- If image build or pull fails, leave the existing volumes untouched and fix
  the image/repository boundary.
- If a new application container is unhealthy, inspect its logs and reconcile
  the exact failing dependency. Roll back to the last known image tag only if
  the current release cannot be repaired safely.
- A migration failure is a deployment failure. Never compensate by deleting or
  recreating the database.

## Deferred Production Hardening

This recovery does not add HTTPS, a domain, Elastic IP, managed databases,
backup automation, multi-instance availability, production email, or OIDC.
Those remain separate production-hardening work.
