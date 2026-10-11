---
title: Troubleshooting
group: Help
summary: Quick checks for the usual problems.
---

## Checks

1. **Is the server healthy?** The health check reports the database, OCR, the job queue, storage and backups. A line that fails tells you where to look.
   ```bash
   curl localhost:8080/healthz
   make logs
   ```
2. **The app can't connect.** The phone and the server must be on the same network, or on the same Tailscale network. Check the address and the port. If you said no to the local network question, allow it in the iPhone's settings for Shop.
3. **Wrong or lost password.** Make a new one on the server.
   ```bash
   docker compose -f deploy/docker-compose.yml exec api shop password generate
   ```
   Five wrong passwords from one address lock it out for 15 minutes.
4. **A receipt stays on processing.** The OCR engine may be down or busy. Check `ocr` and `jobs` in the health check. The first receipt after a start takes longer.
5. **A receipt says Needs review.** The lines don't add up to the printed total. Fix them on the receipt, or confirm anyway. See [Review and fix a receipt](review-a-receipt).
6. **A photo won't import.** Use JPEG, PNG, HEIC or PDF. If the app can't find the edges, retake the photo flat and in good light.
7. **NAS backup is out of date.** The backup folder wasn't available. Mount the share, then run a backup. See [Update, back up and restore](backup).
   ```bash
   make backup
   ```
8. **Old receipts look wrong after an update.** Read them again from their stored text. Receipts you edited are left alone.
   ```bash
   docker compose -f deploy/docker-compose.yml exec api shop reparse
   ```
