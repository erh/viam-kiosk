#!/bin/sh
# Installs the system dependencies viam-kiosk needs (cage + chromium).
# Viam runs this once, as root, the first time the module is installed on a machine.
set -u

log() { echo "viam-kiosk first_run: $*"; }

if ! command -v apt-get >/dev/null 2>&1; then
    log "apt-get not found; install cage and chromium manually for this distro"
    exit 0
fi

export DEBIAN_FRONTEND=noninteractive

apt-get update

if ! command -v cage >/dev/null 2>&1; then
    log "installing cage"
    apt-get install -y --no-install-recommends cage
fi

# The package/binary is "chromium" on Debian and Raspberry Pi OS,
# "chromium-browser" on some older releases.
if ! command -v chromium >/dev/null 2>&1 && ! command -v chromium-browser >/dev/null 2>&1; then
    log "installing chromium"
    apt-get install -y --no-install-recommends chromium || \
        apt-get install -y --no-install-recommends chromium-browser
fi

# wlr-randr lets the module set the display resolution.
if ! command -v wlr-randr >/dev/null 2>&1; then
    log "installing wlr-randr"
    apt-get install -y --no-install-recommends wlr-randr || \
        log "wlr-randr not available; the resolution config option won't work"
fi

# The getty login prompt on tty1 is what shows up whenever cage isn't
# running (and it competes for the console). A kiosk doesn't want it.
if command -v systemctl >/dev/null 2>&1; then
    log "disabling getty on tty1"
    systemctl disable --now getty@tty1.service || true
fi

log "done"
