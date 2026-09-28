#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'usage: %s <function-package> <output-initrd.cpio.gz>\n' "$0" >&2
  printf 'example: %s ./functions/echo-grpc ./bin/firecracker/echo-grpc.cpio.gz\n' "$0" >&2
}

if [[ $# -ne 2 ]]; then
  usage
  exit 2
fi

if [[ $(id -u) -ne 0 ]]; then
  echo "error: root privileges are required to create initramfs device nodes" >&2
  echo "rerun with sudo, preserving Go in PATH if necessary: sudo env PATH=\$PATH $0 ..." >&2
  exit 1
fi

function_pkg="$1"
output_initrd="$2"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d)"
cleanup() {
  rm -rf "$work_dir"
}
trap cleanup EXIT

mkdir -p "$(dirname "$output_initrd")"
output_initrd="$(cd "$(dirname "$output_initrd")" && pwd)/$(basename "$output_initrd")"

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
mkdir -p "$root_dir/proc" "$root_dir/sys" "$root_dir/dev" "$root_dir/tmp" "$root_dir/etc"
cp "$work_dir/init" "$root_dir/init"
cp "$work_dir/function" "$root_dir/function"
chmod 0755 "$root_dir/init" "$root_dir/function"
mknod -m 600 "$root_dir/dev/console" c 5 1
mknod -m 666 "$root_dir/dev/null" c 1 3
chown -R 0:0 "$root_dir"
cat > "$root_dir/etc/resolv.conf" <<'EOF'
nameserver 1.1.1.1
nameserver 8.8.8.8
EOF

(
  cd "$root_dir"
  if [[ "$output_initrd" == *.gz ]]; then
    find . -print0 | cpio --null -ov --format=newc 2>/dev/null | gzip -9 > "$output_initrd"
  else
    find . -print0 | cpio --null -ov --format=newc 2>/dev/null > "$output_initrd"
  fi
)
echo "wrote $output_initrd"
