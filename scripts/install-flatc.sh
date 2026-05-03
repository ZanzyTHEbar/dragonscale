#!/usr/bin/env bash
set -euo pipefail

FLATC_VERSION="${FLATC_VERSION:-25.12.19}"
FLATC_ASSET="${FLATC_ASSET:-Linux.flatc.binary.g++-13.zip}"
FLATC_SHA256="${FLATC_SHA256:-9f87066dc5dfa7fe02090b55bab5f3e55df03e32c9b0cdf229004ade7d091039}"

install_dir="${1:-/usr/local/bin}"

case "$(uname -s)/$(uname -m)" in
	Linux/x86_64|Linux/amd64)
		;;
	*)
		echo "Unsupported flatc pin target: $(uname -s)/$(uname -m)." >&2
		exit 1
		;;
esac

for required in curl find sha256sum unzip install; do
	if ! command -v "$required" >/dev/null 2>&1; then
		echo "Missing required command for flatc install: $required" >&2
		exit 1
	fi
done

tmpdir="$(mktemp -d)"
cleanup() {
	rm -rf "$tmpdir"
}
trap cleanup EXIT
trap 'trap - EXIT INT TERM; cleanup; exit 130' INT
trap 'trap - EXIT INT TERM; cleanup; exit 143' TERM

asset_url_name="${FLATC_ASSET//+/%2B}"
archive="$tmpdir/flatc.zip"
extract_dir="$tmpdir/extract"
url="https://github.com/google/flatbuffers/releases/download/v${FLATC_VERSION}/${asset_url_name}"

mkdir -p "$install_dir" "$extract_dir"
curl --retry 3 --retry-connrefused --retry-delay 2 -fsSL "$url" -o "$archive"
printf '%s  %s\n' "$FLATC_SHA256" "$archive" | sha256sum -c -
unzip -q "$archive" -d "$extract_dir"

flatc_matches=()
while IFS= read -r candidate; do
	flatc_matches+=("$candidate")
done < <(find "$extract_dir" -type f -name flatc)

if [ "${#flatc_matches[@]}" -ne 1 ]; then
	echo "Downloaded FlatBuffers asset must contain exactly one flatc binary; found ${#flatc_matches[@]}." >&2
	exit 1
fi

flatc_bin="${flatc_matches[0]}"
chmod +x "$flatc_bin"
install -m 0755 "$flatc_bin" "$install_dir/flatc"
"$install_dir/flatc" --version
