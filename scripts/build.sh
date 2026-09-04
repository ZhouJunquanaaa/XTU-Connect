#!/usr/bin/env bash
# XTU-Connect 全平台编译脚本
#
# 用法:
#   ./scripts/build.sh                     # 编译全部目标（按宿主机能力）
#   ./scripts/build.sh --version 0.2.0     # 指定版本号（默认取 git tag 或日期）
#
# 产物输出到 dist/：
#   xtu-connect_<ver>_<os>-<arch>[.exe]              CLI（纯 Go，随处可编译）
#   xtu-connect-desktop_<ver>_<os>-<arch>[.exe]      桌面版（Windows/Linux 纯 Go；
#                                                     macOS 需在本机编译，依赖 cgo）
#   XTU-Connect_<ver>_macos_<arch>.zip               macOS 应用包（.app，菜单栏常驻）
#   checksums.txt                                     全部产物的 SHA256
set -euo pipefail
cd "$(dirname "$0")/.."

# ---------- 参数 ----------
VERSION=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

if [[ -z "$VERSION" ]]; then
  VERSION="$(git describe --tags 2>/dev/null || true)"
  [[ -z "$VERSION" ]] && VERSION="dev-$(date +%Y%m%d)"
fi
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
HOST_OS="$(go env GOOS)"
DIST="dist"

echo "==> 版本: $VERSION (commit $COMMIT)"
rm -rf "$DIST" && mkdir -p "$DIST"

LDFLAGS_CLI="-s -w -X main.xtuConnectVersion=$VERSION -X main.CommitID=$COMMIT"
LDFLAGS_GUI="-s -w -X main.desktopVersion=$VERSION -X main.CommitID=$COMMIT"

build_cli() { # os arch
  local os="$1" arch="$2" ext=""
  [[ "$os" == "windows" ]] && ext=".exe"
  echo "==> CLI  $os/$arch"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "$LDFLAGS_CLI" \
    -o "$DIST/xtu-connect_${VERSION}_${os}-${arch}${ext}" .
}

build_gui() { # os arch [extra ldflags]
  local os="$1" arch="$2" extra="${3:-}" ext=""
  [[ "$os" == "windows" ]] && ext=".exe"
  echo "==> 桌面版 $os/$arch"
  if [[ "$os" == "darwin" ]]; then
    # macOS 托盘依赖 Cocoa（cgo），只能在本机编译
    if [[ "$HOST_OS" != "darwin" ]]; then
      echo "    跳过：darwin 桌面版需在 macOS 上编译"
      return 0
    fi
    CGO_ENABLED=1 GOOS="$os" GOARCH="$arch" \
      go build -trimpath -ldflags "$LDFLAGS_GUI $extra" \
      -o "$DIST/xtu-connect-desktop_${VERSION}_${os}-${arch}${ext}" ./cmd/xtu-connect-desktop
  else
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      go build -trimpath -ldflags "$LDFLAGS_GUI $extra" \
      -o "$DIST/xtu-connect-desktop_${VERSION}_${os}-${arch}${ext}" ./cmd/xtu-connect-desktop
  fi
}

# ---------- CLI：全平台 ----------
build_cli darwin arm64
build_cli darwin amd64
build_cli windows amd64
build_cli linux amd64
build_cli linux arm64

# ---------- 桌面版 ----------
build_gui darwin arm64
build_gui darwin amd64
build_gui windows amd64 "-H=windowsgui"   # 无控制台窗口
build_gui linux amd64
build_gui linux arm64

# ---------- macOS .app 应用包 ----------
make_app() { # arch
  local arch="$1"
  local app="XTU-Connect.app"
  local root="$DIST/.appbuild-$arch"
  local bundle="$root/$app"
  [[ -d "$bundle" ]] && rm -rf "$bundle"
  mkdir -p "$bundle/Contents/MacOS" "$bundle/Contents/Resources"

  # 注意：macOS 文件系统大小写不敏感，GUI 与 CLI 的文件名必须不同，
  # 否则 XTU-Connect 与 xtu-connect 会被视为同一路径而互相覆盖
  cp "$DIST/xtu-connect-desktop_${VERSION}_darwin-${arch}" "$bundle/Contents/MacOS/xtu-connect-desktop"
  cp "$DIST/xtu-connect_${VERSION}_darwin-${arch}" "$bundle/Contents/MacOS/xtu-connect"
  cp cmd/xtu-connect-desktop/icon.png "$bundle/Contents/Resources/icon.png"

  # 尽力生成 icns（仅 macOS 宿主可用）
  if command -v iconutil >/dev/null 2>&1; then
    iconset="$root/icon.iconset"
    mkdir -p "$iconset"
    for size in 16 32 64 128 256 512; do
      sips -z "$size" "$size" cmd/xtu-connect-desktop/icon.png \
        --out "$iconset/icon_${size}x${size}.png" >/dev/null 2>&1 || true
    done
    iconutil -c icns "$iconset" -o "$bundle/Contents/Resources/icon.icns" >/dev/null 2>&1 || true
  fi

  cat > "$bundle/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>XTU-Connect</string>
  <key>CFBundleDisplayName</key><string>XTU-Connect</string>
  <key>CFBundleIdentifier</key><string>edu.xtu.connect.desktop</string>
  <key>CFBundleVersion</key><string>${VERSION}</string>
  <key>CFBundleShortVersionString</key><string>${VERSION}</string>
  <key>CFBundleExecutable</key><string>xtu-connect-desktop</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleIconFile</key><string>icon</string>
  <key>LSUIElement</key><true/>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

  if command -v codesign >/dev/null 2>&1; then
    codesign --force --sign - "$bundle" >/dev/null 2>&1 || true
  fi

  (cd "$root" && zip -qry "../XTU-Connect_${VERSION}_macos-${arch}.zip" "$app")
  echo "==> 应用包 macos/$arch: XTU-Connect_${VERSION}_macos-${arch}.zip"
}

if [[ "$HOST_OS" == "darwin" ]]; then
  make_app arm64
  make_app amd64
fi
rm -rf "$DIST/.appbuild-"*

# ---------- 校验和 ----------
( cd "$DIST" && shasum -a 256 * > checksums.txt 2>/dev/null || sha256sum * > checksums.txt )

echo "==> 完成，产物："
ls -lh "$DIST"
