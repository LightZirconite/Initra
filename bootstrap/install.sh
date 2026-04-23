#!/usr/bin/env sh
set -eu

BASE_URL="${INITRA_BASE_URL:-${SETUPCTL_BASE_URL:-https://git.justw.tf/LightZirconite/setup-win}}"
BASE_URL="${BASE_URL%/}"
BIN_DIR="${HOME}/.local/bin"
BIN_PATH="${BIN_DIR}/initra"
CATALOG_DIR="${BIN_DIR}/catalog"
CATALOG_PATH="${CATALOG_DIR}/catalog.yaml"
TMP_PATH="/tmp/initra-linux-amd64"

mkdir -p "${BIN_DIR}"
mkdir -p "${CATALOG_DIR}"

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "${BASE_URL}/releases/initra-linux-amd64" -o "${TMP_PATH}"
  curl -fsSL "${BASE_URL}/releases/catalog/catalog.yaml" -o "${CATALOG_PATH}"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "${TMP_PATH}" "${BASE_URL}/releases/initra-linux-amd64"
  wget -qO "${CATALOG_PATH}" "${BASE_URL}/releases/catalog/catalog.yaml"
else
  echo "curl or wget is required" >&2
  exit 1
fi

chmod +x "${TMP_PATH}"
mv "${TMP_PATH}" "${BIN_PATH}"

exec "${BIN_PATH}" "$@"
