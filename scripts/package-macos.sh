#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-dev}"
GOARCH="${GOARCH:-arm64}"
GOOS="${GOOS:-darwin}"

if [[ "${GOOS}" != "darwin" ]]; then
  echo "package-macos requires GOOS=darwin" >&2
  exit 1
fi

DIST_DIR="${ROOT}/dist"
BUILD_DIR="${DIST_DIR}/build"
APP_DIR="${DIST_DIR}/AGX.app"
DMG_STAGE="${DIST_DIR}/dmg"
CLI_ARCHIVE="${DIST_DIR}/agx-darwin-${GOARCH}.tar.gz"
DMG_PATH="${DIST_DIR}/AGX-darwin-${GOARCH}.dmg"

COMMIT="$(git -C "${ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}"

rm -rf "${BUILD_DIR}" "${APP_DIR}" "${DMG_STAGE}" "${CLI_ARCHIVE}" "${DMG_PATH}"
mkdir -p "${BUILD_DIR}" "${APP_DIR}/Contents/MacOS" "${APP_DIR}/Contents/Resources" "${DMG_STAGE}"

echo "==> Building AGX CLI (${GOOS}/${GOARCH}, ${VERSION})"
(
  cd "${ROOT}"
  GOOS="${GOOS}" GOARCH="${GOARCH}" go build -ldflags "${LDFLAGS}" -o "${BUILD_DIR}/agx" ./cmd/agx
)

echo "==> Building frontend"
npm --prefix "${ROOT}/desktop/frontend" ci
npm --prefix "${ROOT}/desktop/frontend" run build

echo "==> Building AGX Desktop (${GOOS}/${GOARCH}, ${VERSION})"
(
  cd "${ROOT}"
  GOOS="${GOOS}" GOARCH="${GOARCH}" go build -tags "desktop,production" -o "${BUILD_DIR}/AGXDesktop" ./desktop
)

echo "==> Creating AGX.app"
cp "${BUILD_DIR}/AGXDesktop" "${APP_DIR}/Contents/MacOS/AGXDesktop"
cp "${BUILD_DIR}/agx" "${APP_DIR}/Contents/MacOS/agx"
chmod +x "${APP_DIR}/Contents/MacOS/AGXDesktop" "${APP_DIR}/Contents/MacOS/agx"

if [[ -f "${ROOT}/build/appicon.png" ]]; then
  ICONSET="${BUILD_DIR}/AppIcon.iconset"
  mkdir -p "${ICONSET}"
  for size in 16 32 128 256 512; do
    sips -z "${size}" "${size}" "${ROOT}/build/appicon.png" --out "${ICONSET}/icon_${size}x${size}.png" >/dev/null
    retina=$((size * 2))
    sips -z "${retina}" "${retina}" "${ROOT}/build/appicon.png" --out "${ICONSET}/icon_${size}x${size}@2x.png" >/dev/null
  done
  iconutil -c icns "${ICONSET}" -o "${APP_DIR}/Contents/Resources/AppIcon.icns"
fi

cat > "${APP_DIR}/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>en</string>
  <key>CFBundleExecutable</key>
  <string>AGXDesktop</string>
  <key>CFBundleIdentifier</key>
  <string>dev.agx.desktop</string>
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleName</key>
  <string>AGX</string>
  <key>CFBundleDisplayName</key>
  <string>AGX</string>
  <key>CFBundleIconFile</key>
  <string>AppIcon</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>${VERSION#v}</string>
  <key>CFBundleVersion</key>
  <string>${COMMIT}</string>
  <key>LSMinimumSystemVersion</key>
  <string>13.0</string>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
PLIST

echo "APPL????" > "${APP_DIR}/Contents/PkgInfo"

echo "==> Creating CLI archive"
tar -C "${BUILD_DIR}" -czf "${CLI_ARCHIVE}" agx

echo "==> Creating DMG"
cp -R "${APP_DIR}" "${DMG_STAGE}/AGX.app"
ln -s /Applications "${DMG_STAGE}/Applications"
hdiutil create \
  -volname "AGX" \
  -srcfolder "${DMG_STAGE}" \
  -ov \
  -format UDZO \
  "${DMG_PATH}"

echo "==> Release artifacts"
ls -lh "${CLI_ARCHIVE}" "${DMG_PATH}"
