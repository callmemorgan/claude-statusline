#!/usr/bin/env sh
# Sign checksums.txt with the release key and emit a lean, version-stable cosign
# bundle: {"messageSignature":{"signature":"<base64 ASN.1-DER ECDSA>"}}.
#
# Why normalize: cosign's --bundle output nests the signature differently across
# versions — the legacy cosign bundle uses the top-level .base64Signature, while
# the newer sigstore bundle uses .messageSignature.signature. The signature bytes
# are identical. The self-update path verifies only that signature in-process,
# and the deployed v1.2.0 verifier reads .messageSignature.signature specifically.
# Normalizing to that shape here (regardless of the cosign version CI resolves)
# keeps every published release verifiable by already-installed binaries and
# immune to the cosign-version drift that previously broke releases.
#
# Usage: sign-checksums.sh <artifact> <out-bundle>
#   Reads COSIGN_PRIVATE_KEY (PEM) and COSIGN_PASSWORD from the environment.
set -eu

artifact="$1" # path to checksums.txt
out="$2"      # path to write checksums.txt.bundle
raw="${out}.raw"

cosign sign-blob --key=env://COSIGN_PRIVATE_KEY --bundle="$raw" --yes "$artifact"

sig="$(jq -r '.base64Signature // .messageSignature.signature' "$raw")"
if [ -z "$sig" ] || [ "$sig" = "null" ]; then
	echo "sign-checksums: could not extract signature from cosign bundle" >&2
	exit 1
fi

jq -n --arg s "$sig" '{messageSignature:{signature:$s}}' >"$out"
rm -f "$raw"
