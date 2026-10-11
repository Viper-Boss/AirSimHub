#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Install the separately built, static arm64 helper on an accepted UFI003.
set -eu
test "$#" -eq 1 || { echo 'usage: install-carrier-voice.sh HELPER'; exit 2; }
test "$(id -u)" -eq 0
test -f /etc/nassimhub/local-volte.enabled
tr '\000' '\n' </proc/device-tree/compatible | grep -qx thwc,ufi003
test "$(uname -m)" = aarch64
calls=$(mmcli --timeout=3 -m any --voice-list-calls)
test "$calls" = 'No calls were found'
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
test -x "$1"
"$1" get
install -m 0755 "$1" /usr/local/libexec/nassimhub-voice-domain.new
mv -f /usr/local/libexec/nassimhub-voice-domain.new /usr/local/libexec/nassimhub-voice-domain
install -m 0755 "$here/nassimhub-carrier-voice" /usr/local/libexec/nassimhub-carrier-voice
install -m 0644 "$here/nassimhub-carrier-voice.service" /etc/systemd/system/nassimhub-carrier-voice.service
systemctl daemon-reload
systemctl enable --now nassimhub-carrier-voice.service
