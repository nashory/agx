#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT}/dist"
CHECKSUMS="${DIST_DIR}/checksums.txt"

if [[ ! -d "${DIST_DIR}" ]]; then
  echo "dist directory does not exist" >&2
  exit 1
fi

artifacts=()
while IFS= read -r artifact; do
  artifacts+=("dist/$(basename "${artifact}")")
done < <(find "${DIST_DIR}" -maxdepth 1 -type f \( -name "*.tar.gz" -o -name "*.dmg" \) | sort)

if [[ "${#artifacts[@]}" -eq 0 ]]; then
  echo "no release artifacts found in ${DIST_DIR}" >&2
  exit 1
fi

echo "==> Writing checksums"
(
  cd "${ROOT}"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${artifacts[@]}" > "${CHECKSUMS}"
  else
    sha256sum "${artifacts[@]}" > "${CHECKSUMS}"
  fi
)

ls -lh "${CHECKSUMS}"
