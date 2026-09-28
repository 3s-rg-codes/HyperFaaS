#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'usage: %s <function-package> <output-rootfs> [size-mib]\n' "$0" >&2
  printf 'example: %s ./functions/go/echo-grpc ./bin/firecracker/echo-grpc.ext4\n' "$0" >&2
}

if [[ $# -lt 2 || $# -gt 3 ]]; then
  usage
  exit 2
fi

function_pkg="$1"
output_rootfs="$2"
size_mib="${3:-}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
. "$repo_root/scripts/lib/ext4-size.sh"
work_dir="$(mktemp -d -p "$repo_root")"
cleanup() {
  rm -rf "$work_dir"
}
trap cleanup EXIT

mkdir -p "$(dirname "$output_rootfs")"

echo "building static firecracker init"
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$work_dir/init" ./cmd/firecracker-init
)

echo "building static function binary from $function_pkg"
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$work_dir/function" "$function_pkg"
)

root_dir="$work_dir/root"
mkdir -p "$root_dir/sbin" "$root_dir/proc" "$root_dir/sys" "$root_dir/dev" "$root_dir/tmp" "$root_dir/etc"
cp "$work_dir/init" "$root_dir/sbin/init"
cp "$work_dir/function" "$root_dir/function"
chmod 0755 "$root_dir/sbin/init" "$root_dir/function"
cat > "$root_dir/etc/resolv.conf" <<'EOF'
nameserver 1.1.1.1
nameserver 8.8.8.8
EOF

if [[ -z "$size_mib" ]]; then
  size_mib="$(compute_ext4_size_mib "$root_dir")"
fi
echo "creating ${size_mib}MiB ext4 rootfs"

if [[ $(id -u) -ne 0 ]]; then
  echo "No root privileges. Using Docker to build ext4 rootfs..."
  abs_work_dir="$(cd "$work_dir" && pwd)"
  docker run --rm --privileged \
    -v "$abs_work_dir:/work" \
    -w /work \
    alpine:latest sh -c "
      mkdir -p root/dev && \
      mknod -m 600 root/dev/console c 5 1 && \
      mknod -m 666 root/dev/null c 1 3 && \
      chown -R 0:0 root && \
      truncate -s ${size_mib}M rootfs.ext4 && \
      apk add --no-cache e2fsprogs && \
      mkfs.ext4 -q -F -d root rootfs.ext4 && \
      chown -R $(id -u):$(id -g) .
    "
  mv "$work_dir/rootfs.ext4" "$output_rootfs"
  echo "wrote $output_rootfs"
else
  mknod -m 600 "$root_dir/dev/console" c 5 1
  mknod -m 666 "$root_dir/dev/null" c 1 3
  chown -R 0:0 "$root_dir"

  tmp_image="$work_dir/rootfs.ext4"
  truncate -s "${size_mib}M" "$tmp_image"
  mkfs.ext4 -q -F -d "$root_dir" "$tmp_image"
  mv "$tmp_image" "$output_rootfs"
  echo "wrote $output_rootfs"
fi
