#!/usr/bin/env sh
set -eu

BASE_URL="${INITRA_BASE_URL:-${SETUPCTL_BASE_URL:-https://git.justw.tf/LightZirconite/setup-win/raw/branch/main}}"
BASE_URL="${BASE_URL%/}"
BIN_DIR="${HOME}/.local/bin"
BIN_PATH="${BIN_DIR}/initra"
CATALOG_DIR="${BIN_DIR}/catalog"
CATALOG_PATH="${CATALOG_DIR}/catalog.yaml"
TMP_PATH="/tmp/initra-linux-amd64"
MANIFEST_PATH="/tmp/initra-latest.json"

mkdir -p "${BIN_DIR}"
mkdir -p "${CATALOG_DIR}"

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "${BASE_URL}/releases/latest.json" -o "${MANIFEST_PATH}"
  ARTIFACT_URL="$(sed -n 's/.*"linux-amd64"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${MANIFEST_PATH}" | head -n 1)"
  EXPECTED_SHA256="$(sed -n 's/.*"linux-amd64"[[:space:]]*:[[:space:]]*"\([0-9a-fA-F]\{64\}\)".*/\1/p' "${MANIFEST_PATH}" | tail -n 1 | tr '[:upper:]' '[:lower:]')"
  [ -n "${ARTIFACT_URL}" ] || ARTIFACT_URL="${BASE_URL}/releases/initra-linux-amd64"
  curl -fsSL "${ARTIFACT_URL}" -o "${TMP_PATH}"
  curl -fsSL "${BASE_URL}/releases/catalog/catalog.yaml" -o "${CATALOG_PATH}"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "${MANIFEST_PATH}" "${BASE_URL}/releases/latest.json"
  ARTIFACT_URL="$(sed -n 's/.*"linux-amd64"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${MANIFEST_PATH}" | head -n 1)"
  EXPECTED_SHA256="$(sed -n 's/.*"linux-amd64"[[:space:]]*:[[:space:]]*"\([0-9a-fA-F]\{64\}\)".*/\1/p' "${MANIFEST_PATH}" | tail -n 1 | tr '[:upper:]' '[:lower:]')"
  [ -n "${ARTIFACT_URL}" ] || ARTIFACT_URL="${BASE_URL}/releases/initra-linux-amd64"
  wget -qO "${TMP_PATH}" "${ARTIFACT_URL}"
  wget -qO "${CATALOG_PATH}" "${BASE_URL}/releases/catalog/catalog.yaml"
else
  echo "curl or wget is required" >&2
  exit 1
fi

if [ -n "${EXPECTED_SHA256:-}" ]; then
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL_SHA256="$(sha256sum "${TMP_PATH}" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    ACTUAL_SHA256="$(shasum -a 256 "${TMP_PATH}" | awk '{print $1}')"
  else
    echo "sha256sum or shasum is required to verify the downloaded Initra binary" >&2
    exit 1
  fi
  if [ "${ACTUAL_SHA256}" != "${EXPECTED_SHA256}" ]; then
    rm -f "${TMP_PATH}"
    echo "Downloaded Initra binary failed integrity verification." >&2
    echo "Expected SHA-256: ${EXPECTED_SHA256}" >&2
    echo "Actual SHA-256:   ${ACTUAL_SHA256}" >&2
    exit 1
  fi
fi

chmod +x "${TMP_PATH}"
mv "${TMP_PATH}" "${BIN_PATH}"

exec "${BIN_PATH}" "$@"
