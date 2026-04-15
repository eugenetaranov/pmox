#!/bin/sh
# Example --post-create script for pmox launch / pmox clone.
#
# Invoked directly (not via a shell wrapper) once the VM has an IP and
# pmox has confirmed SSH is reachable. The following env vars are set:
#
#   PMOX_IP    IPv4 address reported by qemu-guest-agent
#   PMOX_VMID  numeric VMID assigned by Proxmox
#   PMOX_NAME  VM name passed to pmox launch
#   PMOX_USER  SSH login user (server config 'user', else 'pmox')
#   PMOX_NODE  Proxmox node the VM was placed on
#
# Exit 0 on success. Any non-zero exit is treated as a warning by
# default; use --strict-hooks on the launch/clone command line to
# upgrade the failure to exit code 8 (ExitHook).
#
# Usage:
#   pmox launch --post-create ./examples/post-create.sh web1

set -eu

: "${PMOX_IP:?PMOX_IP not set}"
: "${PMOX_USER:=pmox}"

echo "post-create: waiting for cloud-init on ${PMOX_NAME:-$PMOX_IP}"
ssh \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    "${PMOX_USER}@${PMOX_IP}" \
    'cloud-init status --wait'
echo "post-create: cloud-init done on ${PMOX_NAME:-$PMOX_IP}"
