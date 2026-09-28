#!/usr/bin/env bash

# Size an ext4 image to fit rootfs content with a small metadata headroom.
compute_ext4_size_mib() {
  local root_dir="$1"
  local bytes mib min_mib=8 headroom_mib=4
  bytes=$(du -sb "$root_dir" | awk '{print $1}')
  mib=$(( (bytes + headroom_mib * 1024 * 1024) / 1024 / 1024 ))
  if [ "$mib" -lt "$min_mib" ]; then
    mib=$min_mib
  fi
  echo "$mib"
}
