# GCP Observability Benchmark Environment

## Purpose

This disposable environment supplies controlled Linux ARM64 capacity for the
PostgreSQL, Kafka, and ClickHouse observability migration evaluation. It is not
the Nano production environment and must not receive production data.

## Inventory

| Resource | Value |
|---|---|
| Project | `nano-obs-bench-20260817` |
| gcloud configuration | `nano-bench` |
| Region / zone | `asia-southeast1` / `asia-southeast1-b` |
| VPC / subnet | `nano-bench-vpc` / `nano-bench-sea` (`10.42.0.0/24`) |
| SUT | `nano-bench-sut`, `c4a-standard-8`, `10.42.0.10` |
| Load generator | `nano-bench-loadgen`, `c4a-standard-4`, `10.42.0.20` |
| Boot disks | 25 GiB Hyperdisk Balanced per VM, auto-delete with VM |
| SUT data disk | `nano-bench-sut-data`, 100 GiB Hyperdisk Balanced, 12,000 IOPS, 500 MiB/s |
| Data mount | `/mnt/nano-bench-data`, ext4, `noatime` |
| Image | Ubuntu 24.04 LTS ARM64; initial resolved image `ubuntu-2404-noble-arm64-v20260807` |

Both VMs are standard on-demand instances with no external IP and no attached
service account. IAP provides SSH access. Cloud NAT provides outbound access,
and benchmark traffic stays on the private subnet. A project-scoped USD 50
monthly budget alerts at 50%, 90%, and 100%; it is not a hard spending cap.

## Select the environment

```bash
gcloud config configurations activate nano-bench
gcloud config list
```

The local `default` gcloud configuration intentionally has no project. Do not
run benchmark commands against an implicit project.

## Start and stop

Start both VMs only for setup or experiments:

```bash
gcloud compute instances start nano-bench-sut nano-bench-loadgen
```

Stop both VMs when work ends:

```bash
gcloud compute instances stop nano-bench-sut nano-bench-loadgen
```

Stopping removes VM compute charges but not disk, image, snapshot, external
address, or network-service charges. Check Billing rather than treating a
stopped VM as a zero-cost project.

## Connect through IAP

```bash
gcloud compute ssh nano-bench-sut --tunnel-through-iap
gcloud compute ssh nano-bench-loadgen --tunnel-through-iap
```

No SSH firewall rule accepts arbitrary internet sources. The IAP rule permits
TCP/22 only from `35.235.240.0/20` to VMs tagged `nano-bench`.

## Verify inventory and isolation

```bash
gcloud compute instances list \
  --filter='labels.purpose=observability-benchmark'
gcloud compute disks list
gcloud compute addresses list
```

From the load generator, verify private reachability:

```bash
ping -c 3 10.42.0.10
```

On the SUT, verify the controlled data volume:

```bash
findmnt /mnt/nano-bench-data
df -h /mnt/nano-bench-data
ls -l /dev/disk/by-id/google-nano-bench-sut-data
```

The SUT metadata startup script is idempotent: it formats only a disk with no
filesystem signature, rejects an unexpected filesystem, and otherwise mounts
the existing ext4 filesystem. It must never format a recognized filesystem.

## Calibrate and freeze storage

The 100 GiB data volume is a calibration starting point, not the authoritative
A/B/C capacity. Load a production-shaped fixture, measure the peak stable disk
footprint for each stage, and choose:

```text
final capacity = 2 x largest measured stable footprint
```

Hyperdisk can grow but cannot shrink. Grow it only after calibration, expand
ext4, and record the final size before Stage A. Do not change capacity, IOPS, or
throughput between A, B, and C.

## Teardown boundary

Benchmark data is disposable, but teardown is destructive. Before deletion,
verify the active gcloud configuration, project, exact instance names, disk
users, and whether raw benchmark artifacts have been exported. The project is
the isolation boundary; deleting project `nano-obs-bench-20260817` ultimately
removes the VMs, disks, network, budget-scoped resources, and benchmark data.
Never run teardown through the production Nano AWS access path.
