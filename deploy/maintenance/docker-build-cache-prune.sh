#!/usr/bin/env bash
# Build cache only: never delete images, volumes, test data, or backups.
set -Eeuo pipefail
umask 077
exec 9>/run/lock/sub2api-build-cache-prune.lock
flock -n 9 || exit 0
docker builder prune --force --filter 'until=168h' --keep-storage 2GB
