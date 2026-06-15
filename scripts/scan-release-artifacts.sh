#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${1:-${ROOT}/dist}"

if [ ! -d "${DIST_DIR}" ]; then
  echo "dist directory does not exist: ${DIST_DIR}" >&2
  exit 1
fi

artifacts=()
while IFS= read -r artifact; do
  artifacts+=("${artifact}")
done < <(find "${DIST_DIR}" -maxdepth 1 -type f \( -name '*.tar.gz' -o -name '*.dmg' \) | sort)
if [ "${#artifacts[@]}" -eq 0 ]; then
  echo "no release artifacts found in ${DIST_DIR}" >&2
  exit 1
fi

forbidden_path_regex='(^|/)(\.agx|\.env|\.env\..*|config\.toml|agx\.db|runtime\.db|discord\.lock|internal_docs)(/|$)|(^|/).*(token|secret|credential).*\.(env|json|toml|txt)$'

scan_listing() {
  local label="$1"
  local listing="$2"
  local matches
  matches="$(printf '%s\n' "${listing}" | grep -E "${forbidden_path_regex}" || true)"
  if [ -n "${matches}" ]; then
    echo "release artifact contains forbidden local or secret-like paths: ${label}" >&2
    printf '%s\n' "${matches}" >&2
    exit 1
  fi
}

scan_tarball() {
  local artifact="$1"
  local listing
  listing="$(tar -tzf "${artifact}")"
  scan_listing "${artifact}" "${listing}"
}

scan_dmg() {
  local artifact="$1"
  if ! command -v hdiutil >/dev/null 2>&1; then
    echo "skipping DMG content scan without hdiutil: ${artifact}" >&2
    return
  fi
  local mount
  mount="$(mktemp -d)"
  local attached=0
  cleanup() {
    if [ "${attached}" -eq 1 ]; then
      hdiutil detach "${mount}" -quiet >/dev/null 2>&1 || true
    fi
    rmdir "${mount}" >/dev/null 2>&1 || true
  }
  trap cleanup RETURN
  hdiutil attach "${artifact}" -readonly -nobrowse -mountpoint "${mount}" -quiet
  attached=1
  local listing
  listing="$(cd "${mount}" && find . -print | sed 's#^\./##')"
  scan_listing "${artifact}" "${listing}"
}

for artifact in "${artifacts[@]}"; do
  case "${artifact}" in
    *.tar.gz) scan_tarball "${artifact}" ;;
    *.dmg) scan_dmg "${artifact}" ;;
  esac
done

echo "release artifact scan passed"
