#!/bin/sh
# Checks the licences of the Swift client's resolved dependency closure
# against the ADR 0006 allowlist (MIT, BSD, Apache-2.0, ISC).
#
# Apple platforms build no dependency (CryptoKit and CommonCrypto are in the
# OS). Linux and the other platforms build swift-crypto (Apache-2.0) and its
# dependency swift-asn1 (Apache-2.0). swift-crypto vendors C code, checked
# file by file below:
#
#   - CCryptoBoringSSL: BoringSSL, Apache-2.0 (relicensed from the OpenSSL/ISC
#     mix in 2024), with fiat-crypto (MIT OR Apache-2.0 OR BSD-1-Clause).
#   - CXKCP: the eXtended Keccak Code Package (SHA-3, ML-KEM), CC0-1.0.
#     EXCEPTION: CC0-1.0 is a public-domain dedication, more permissive than
#     MIT; it is linked on non-Apple platforms only (also in clients/swift/README.md).
#
#   scripts/check-licenses.sh [scratch path]   # default .build
set -eu
cd "$(dirname "$0")/.."
scratch="${1:-.build}"
swift="${XI_SWIFT:-swift}"

$swift package --package-path ../.. --scratch-path "$scratch" resolve >/dev/null
resolved=../../Package.resolved
[ -f "$resolved" ] || { echo "no Package.resolved after resolve" >&2; exit 1; }

# Classifies a licence file by its text.
classify() {
  if grep -q "Apache License" "$1" && grep -q "Version 2.0" "$1"; then echo Apache-2.0
  elif grep -q "Permission is hereby granted, free of charge" "$1"; then echo MIT
  elif grep -q "Permission to use, copy, modify, and/or distribute" "$1"; then echo ISC
  elif grep -q "Redistribution and use in source and binary forms" "$1"; then echo BSD
  else echo unknown
  fi
}

fail=0
pins=0
for location in $(sed -n 's/.*"location" : "\(.*\)".*/\1/p' "$resolved"); do
  name=$(basename "$location" .git)
  dir="$scratch/checkouts/$name"
  file=$(find "$dir" -maxdepth 1 -iname 'LICENSE*' | head -n 1)
  if [ -z "$file" ]; then
    echo "FAIL $name: no LICENSE file in $dir" >&2
    fail=1
    continue
  fi
  lic=$(classify "$file")
  pins=$((pins + 1))
  case "$lic" in
    MIT | BSD | Apache-2.0 | ISC) echo "ok   $name ($location): $lic" ;;
    *) echo "FAIL $name ($location): $lic ($file)" >&2; fail=1 ;;
  esac
done

# Vendored C sources of swift-crypto: every licence statement must be allowed
# (or the documented CC0-1.0 exception of CXKCP).
crypto="$scratch/checkouts/swift-crypto/Sources"
if [ -d "$crypto" ]; then
  if grep -rIl -E "GNU (Lesser )?General Public License|Mozilla Public License|SPDX-License-Identifier: *(L?GPL|AGPL|MPL)" "$crypto" >/dev/null 2>&1; then
    echo "FAIL swift-crypto: copyleft licence text in vendored sources:" >&2
    grep -rIl -E "GNU (Lesser )?General Public License|Mozilla Public License|SPDX-License-Identifier: *(L?GPL|AGPL|MPL)" "$crypto" >&2
    fail=1
  fi
  if [ -d "$crypto/CXKCP" ]; then
    others=$(grep -rIh -o -E "creativecommons.org/[a-z/0-9.]+|SPDX-License-Identifier: *[A-Za-z0-9.-]+" "$crypto/CXKCP" | sort -u | grep -v -E "publicdomain/zero/1.0|Apache-2.0" || true)
    if [ -n "$others" ]; then
      echo "FAIL swift-crypto CXKCP: unexpected licences: $others" >&2
      fail=1
    else
      echo "ok   swift-crypto/CXKCP: CC0-1.0 (documented exception, non-Apple platforms only)"
    fi
  fi
  echo "ok   swift-crypto/CCryptoBoringSSL: $(grep -rIl 'Licensed under the Apache License' "$crypto/CCryptoBoringSSL" | wc -l | tr -d ' ') files Apache-2.0, no copyleft text"
fi

[ "$pins" -gt 0 ] || { echo "no pins in $resolved" >&2; exit 1; }
[ "$fail" -eq 0 ] || exit 1
echo "licences: $pins packages allowed (MIT, BSD, Apache-2.0, ISC; exception CC0-1.0 for swift-crypto's vendored XKCP)"
