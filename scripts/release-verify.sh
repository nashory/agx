#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${AGX_RELEASE_DIST_DIR:-${ROOT}/dist}"
REQUIRE_ARTIFACTS="${AGX_REQUIRE_RELEASE_ARTIFACTS:-0}"

run() {
  echo "==> $*"
  "$@"
}

cd "${ROOT}"

run go test ./...
run npm --prefix desktop/frontend test
run npm --prefix desktop/frontend run build
run go build -ldflags "${AGX_RELEASE_LDFLAGS:-}" -o bin/agx ./cmd/agx
run go build -tags "${AGX_DESKTOP_TAGS:-desktop,production}" -o bin/agx-desktop ./desktop

run ./bin/agx --help
run ./bin/agx --version
run ./bin/agx runtime --help
run ./bin/agx doctor

test -x bin/agx-desktop
test -f desktop/frontend/dist/index.html
test -n "$(find desktop/frontend/dist/assets -name 'index-*.js' -print -quit)"

if find "${DIST_DIR}" -maxdepth 1 -type f \( -name '*.tar.gz' -o -name '*.dmg' \) -print -quit 2>/dev/null | grep -q .; then
  run "${ROOT}/scripts/scan-release-artifacts.sh" "${DIST_DIR}"
else
  if [ "${REQUIRE_ARTIFACTS}" = "1" ]; then
    echo "release artifacts are required but none were found in ${DIST_DIR}" >&2
    exit 1
  fi
  echo "==> Skipping artifact scan; no release artifacts found in ${DIST_DIR}"
fi

echo "release verification passed"
