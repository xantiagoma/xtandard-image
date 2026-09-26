#!/bin/sh
# Seeds the acceptance S3 store (compose.acceptance.yml, service s3-seed):
# creates bucket $S3_BUCKET and uploads every file under /fixtures with
# curl's built-in SigV4 signing, path-style, so no mc/aws CLI image is needed.
# Runs inside the RustFS image (busybox sh + curl). Idempotent: an existing
# bucket owned by us (409 BucketAlreadyOwnedByYou) is fine, objects are
# overwritten.
set -eu

: "${S3_ENDPOINT:?}" "${S3_BUCKET:?}" "${S3_ACCESS_KEY:?}" "${S3_SECRET_KEY:?}"
region="${S3_REGION:-us-east-1}"

s3() {
  curl -sS -o /dev/null -w '%{http_code}' \
    --aws-sigv4 "aws:amz:${region}:s3" --user "${S3_ACCESS_KEY}:${S3_SECRET_KEY}" "$@"
}

code="$(s3 -X PUT "${S3_ENDPOINT}/${S3_BUCKET}")"
case "$code" in
  200 | 409) ;;
  *) echo "seed-s3: create bucket ${S3_BUCKET}: HTTP ${code}" >&2; exit 1 ;;
esac

n=0
for f in $(cd /fixtures && find . -type f | sed 's|^\./||' | sort); do
  case "$f" in
    *.jpg | *.jpeg) ctype=image/jpeg ;;
    *.png) ctype=image/png ;;
    *.gif) ctype=image/gif ;;
    *.webp) ctype=image/webp ;;
    *.avif) ctype=image/avif ;;
    *.heic) ctype=image/heic ;;
    *.jxl) ctype=image/jxl ;;
    *.jp2) ctype=image/jp2 ;;
    *.tif | *.tiff) ctype=image/tiff ;;
    *.bmp) ctype=image/bmp ;;
    *.ico) ctype=image/vnd.microsoft.icon ;;
    *.svg) ctype=image/svg+xml ;;
    *) ctype=application/octet-stream ;;
  esac
  code="$(s3 -X PUT -H "Content-Type: ${ctype}" --data-binary "@/fixtures/${f}" "${S3_ENDPOINT}/${S3_BUCKET}/${f}")"
  if [ "$code" != 200 ]; then
    echo "seed-s3: put ${S3_BUCKET}/${f}: HTTP ${code}" >&2
    exit 1
  fi
  n=$((n + 1))
done
echo "seed-s3: ${n} objects in ${S3_BUCKET}"
