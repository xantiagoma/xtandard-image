#!/bin/sh
# Signs an imgproxy-dialect path with the imgproxy scheme (docs/URL-DIALECTS.md
# §2): base64url(HMAC-SHA256(key, salt || path)) without padding, truncated to
# the signature size. Prints the signed path "/<signature><path>".
#
#   XI_SIGNING_KEYS=<hex> XI_SIGNING_SALTS=<hex> \
#     scripts/sign-url.sh '/rs:fit:640:0/plain/https://example.com/a.jpg@webp'
#
# Key and salt are the first entries of XI_SIGNING_KEYS / XI_SIGNING_SALTS
# (falling back to IMGPROXY_KEY / IMGPROXY_SALT), so the same values sign
# URLs for xtandard-image and imgproxy. XI_SIGNING_SIZE (default 32) truncates
# the signature like imgproxy's IMGPROXY_SIGNATURE_SIZE.
# Needs openssl, awk, head and tr.
set -eu

path="${1:?usage: sign-url.sh <path starting with />}"
case "$path" in /*) ;; *) path="/$path" ;; esac
key="${XI_SIGNING_KEYS:-${IMGPROXY_KEY:-}}"
salt="${XI_SIGNING_SALTS:-${IMGPROXY_SALT:-}}"
key="${key%%,*}"
salt="${salt%%,*}"
size="${XI_SIGNING_SIZE:-32}"
[ -n "$key" ] && [ -n "$salt" ] || {
  echo "sign-url: set XI_SIGNING_KEYS and XI_SIGNING_SALTS (hex)" >&2
  exit 2
}

# hex → octal printf escapes (no xxd needed): "a1ff" → "\241\377".
octal() {
  printf '%s' "$1" | tr 'A-F' 'a-f' | awk '{
    if (length($0) % 2 != 0 || $0 !~ /^[0-9a-f]*$/) { print "bad hex" > "/dev/stderr"; exit 1 }
    for (i = 1; i < length($0); i += 2) {
      v = (index("0123456789abcdef", substr($0, i, 1)) - 1) * 16 + index("0123456789abcdef", substr($0, i + 1, 1)) - 1
      printf "\\%03o", v
    }
  }'
}

salt_esc="$(octal "$salt")"
octal "$key" >/dev/null
# shellcheck disable=SC2059 # the format string is the escaped salt on purpose
sig="$({ printf "$salt_esc"; printf '%s' "$path"; } |
  openssl dgst -sha256 -mac HMAC -macopt "hexkey:$key" -binary |
  head -c "$size" | openssl base64 -A | tr '+/' '-_' | tr -d '=')"
printf '/%s%s\n' "$sig" "$path"
