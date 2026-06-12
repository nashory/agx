#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-dev}"
GOOS="${GOOS:-linux}"
GOARCHES="${GOARCHES:-amd64 arm64}"

if [[ "${GOOS}" != "linux" ]]; then
  echo "package-linux requires GOOS=linux" >&2
  exit 1
fi

DIST_DIR="${ROOT}/dist"
BUILD_DIR="${DIST_DIR}/linux-build"

COMMIT="$(git -C "${ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}"

rm -rf "${BUILD_DIR}"
rm -f "${DIST_DIR}"/agx-linux-*.tar.gz
mkdir -p "${BUILD_DIR}" "${DIST_DIR}"

for arch in ${GOARCHES}; do
  case "${arch}" in
    amd64|arm64) ;;
    *)
      echo "unsupported Linux architecture: ${arch}" >&2
      exit 1
      ;;
  esac

  stage="${BUILD_DIR}/agx-linux-${arch}"
  archive="${DIST_DIR}/agx-linux-${arch}.tar.gz"
  rm -rf "${stage}" "${archive}"
  mkdir -p "${stage}"

  echo "==> Building AGX CLI (linux/${arch}, ${VERSION})"
  (
    cd "${ROOT}"
    CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" go build -ldflags "${LDFLAGS}" -o "${stage}/agx" ./cmd/agx
  )

  cp "${ROOT}/LICENSE" "${stage}/LICENSE"
  cp "${ROOT}/README.md" "${stage}/README.md"
  cp "${ROOT}/docs/INSTALL.md" "${stage}/INSTALL.md"
  chmod +x "${stage}/agx"

  echo "==> Creating ${archive}"
  COPYFILE_DISABLE=1 tar --no-xattrs -C "${stage}" -czf "${archive}" agx LICENSE README.md INSTALL.md
done

echo "==> Release artifacts"
ls -lh "${DIST_DIR}"/agx-linux-*.tar.gz
