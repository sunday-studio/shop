# Capacity notes for the hosted box

Numbers marked (assumed) are estimates, not measurements. Measure on the real server before launch.

## Memory budget (one server, everything in Docker)

| Part | Limit in the compose file | Typical use | Notes |
|---|---|---|---|
| OCR (PaddleOCR PP-OCRv5, 1 worker) | `OCR_MEM_LIMIT` 3g, no swap | 1.5 to 3 GB (assumed) | The big one. The limit is a ceiling: hit it and Docker kills and restarts only OCR; receipts retry |
| API | `API_MEM_LIMIT` 768m | 50 to 200 MB (assumed) | Go, SQLite. Grows with households (per-household connections) |
| Caddy | 128m | 20 to 40 MB | |
| backup | 256m | idle ~20 MB, snapshot peaks | |
| offsite (rclone) | 256m | idle ~20 MB, peaks while copying | |
| monitor | 64m | ~5 MB | |
| OS + Docker | | 300 to 500 MB | |

Sum of limits: about 4.5 GB, so a 4 GB machine is the bare minimum and only works because use stays under the limits. Recommendation: **8 GB RAM, 4 vCPU, 2 GB swap** (swap is for the OS; OCR has none on purpose). That leaves room for a second OCR container. OCR speed on UpCloud vCPUs is unmeasured: after day one upload the 13-receipt set, time it, and set an alert threshold from that (`QUEUE_ALERT_PENDING`).

## Disk growth per household

- A receipt photo is about **1.8 MB before recompression**. After recompression to a smaller JPEG it is probably 0.3 to 0.6 MB (assumed; the recompression belongs to the hosted layer).
- The database is small: tens of KB per receipt (assumed), well under the images.

| Receipts per household per year | Images raw (1.8 MB) | Images recompressed (0.5 MB, assumed) |
|---|---|---|
| 100 | 180 MB | 50 MB |
| 250 | 450 MB | 125 MB |
| 500 | 900 MB | 250 MB |

The backup volume keeps an append-only mirror of every image on the same disk, so **local disk use is about twice the image size**, plus 30 daily database snapshots. The off-site copy adds the same again in the bucket (encrypted, about the same size). Example, 250 receipts a year raw: 1000 households is about 450 GB of images, about 900 GB local with the mirror. Recompression, or not mirroring images locally, is the first lever. Alert at 80 percent is configured (`DISK_ALERT_PCT`); on UpCloud, block storage can be grown without rebuilding the server.

## Knobs, in the order to reach for them

1. **Recompress images** on upload (hosted layer): 3x to 4x less disk, bucket and backup time.
2. **Bigger box** (more vCPU and RAM): the simplest; OCR is CPU-bound, so vCPUs help directly.
3. **Second OCR worker.** Raise capacity by running two OCR containers: `./compose.sh up -d --scale ocr=2` after setting `OCR_MEM_LIMIT` per container; both containers share the network name `ocr`, Docker DNS spreads the connections (long-lived keep-alive connections may stick to one; verify with the queue length under load). Budget another 3 GB. Alternative with less memory: one container with `--workers 2` is not supported by the image today. Whether the API runs more than one job worker is a setting of the API/hosted layer (not verified here).
4. **Object storage for images (option, not needed at launch).** UpCloud Managed Object Storage is S3-compatible, so the existing rclone approach already covers it for BACKUPS (`offsite.env` endpoint/bucket). Using it as the primary image store needs an image-store interface in the API (`notes/hosted-plan.md` section 8 says not in the first cut); the benefit is that the server disk then holds only databases, and images scale with the bucket instead. Mounting a bucket with a FUSE tool is not recommended. Decide on prices and region from UpCloud's current pricing page (not verified here); keep bucket versioning and object lock OFF unless you add a lifecycle rule, or deletions will not propagate and erasure promises break.
5. **Split OCR onto its own server** (`OCR_URL` pointing at it) when one box cannot hold both; the OCR service has no state.

## Decision for the owner

Launch on one 8 GB / 4 vCPU box with images on disk and the S3 bucket for encrypted backups only. Move to object storage for images when disk use passes about 60 percent of a box you are willing to pay for, or when one box can no longer hold both the data and a second OCR worker.
