#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'usage: %s <dockerfile> <output-dir> [docker-build-arg...]\n' "$0" >&2
  printf 'example: %s docker/python-function.Dockerfile ./artifacts/echo-http\n' "$0" >&2
}

if [[ $# -lt 2 ]]; then
  usage
  exit 2
fi

dockerfile="$1"
output_dir="$2"
shift 2

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tag="hfaas-export-rootfs-$$"

cleanup() {
  docker rm -f "$tag" >/dev/null 2>&1 || true
  docker rmi "$tag" >/dev/null 2>&1 || true
}
trap cleanup EXIT

build_args=()
for arg in "$@"; do
  build_args+=(--build-arg "$arg")
done

(
  cd "$repo_root"
  docker build -t "$tag" -f "$dockerfile" "${build_args[@]}" .
)

rm -rf "$output_dir"
mkdir -p "$output_dir"
cid=$(docker create "$tag")
docker export "$cid" | tar -xf - -C "$output_dir"
docker rm "$cid" >/dev/null
echo "wrote $output_dir"
