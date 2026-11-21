#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

if [ ! -f .env ]; then
    echo "Error: No se encontró el archivo .env. Copiá .env.example a .env y completá tus datos."
    exit 1
fi

set -a
source .env
set +a

APP_NAME="JellyfinCliente"
DIST_DIR="dist"
ZIP_NAME="JellyfinCliente-Windows-v2.zip"

echo "[1/3] Preparando cabeceras de Windows y dependencias Go..."
ln -sfn "$PWD/.wininclude" /tmp/wininclude
go mod tidy

echo "[2/3] Compilando $APP_NAME.exe directamente en $DIST_DIR/..."
mkdir -p "$DIST_DIR"

LDFLAGS="-H=windowsgui \
  -X 'main.TailscaleAuthKey=${TAILSCALE_AUTH_KEY}' \
  -X 'main.RemoteTargetURL=${REMOTE_TARGET_URL}' \
  -X 'main.LocalHostsRaw=${LOCAL_HOSTS}'"

GOOS=windows GOARCH=amd64 CGO_ENABLED=1 \
    CC=x86_64-w64-mingw32-gcc \
    CXX=x86_64-w64-mingw32-g++ \
    CGO_CXXFLAGS="-I/tmp/wininclude" \
    CGO_LDFLAGS="-static-libgcc -static-libstdc++" \
    go build -ldflags="$LDFLAGS" -o "$DIST_DIR/$APP_NAME.exe" .

echo "[3/3] Verificando dependencias binarias en $DIST_DIR/..."

# 3.1 Microsoft Edge WebView2 Runtime
if [ ! -f "$DIST_DIR/MicrosoftEdgeWebView2Setup.exe" ]; then
    echo "  -> Descargando MicrosoftEdgeWebView2Setup.exe..."
    curl -fL -o "$DIST_DIR/MicrosoftEdgeWebView2Setup.exe" \
        "https://go.microsoft.com/fwlink/p/?LinkId=2124703"
fi

# 3.2 Instalador oficial de Tailscale (Windows x64)
if [ ! -f "$DIST_DIR/tailscale-setup-amd64.msi" ]; then
    echo "  -> Descargando tailscale-setup-amd64.msi..."
    curl -fL -o "$DIST_DIR/tailscale-setup-amd64.msi" \
        "https://pkgs.tailscale.com/stable/tailscale-setup-latest-amd64.msi"
fi

# 3.3 Reproductor mpv (último build estable x86_64 de shinchiro)
if [ ! -f "$DIST_DIR/mpv.exe" ]; then
    if ! command -v 7z >/dev/null 2>&1; then
        echo "Error: Se requiere '7z' para extraer mpv (instalalo con: sudo pacman -S 7zip)."
        exit 1
    fi

    echo "  -> Buscando última versión de mpv para Windows x86_64..."
    MPV_URL=$(curl -fsSL "https://api.github.com/repos/shinchiro/mpv-winbuild-cmake/releases/latest" \
        | grep -oE '"browser_download_url":\s*"[^"]+mpv-x86_64-[^"]+\.7z"' \
        | grep -v "v3" \
        | head -n 1 \
        | cut -d'"' -f4)

    if [ -z "$MPV_URL" ]; then
        echo "Error: No se pudo obtener la URL de descarga de mpv desde GitHub."
        exit 1
    fi

    TMP_MPV_DIR=$(mktemp -d)
    echo "  -> Descargando y extrayendo mpv..."
    curl -fL -o "$TMP_MPV_DIR/mpv.7z" "$MPV_URL"
    7z x -y "$TMP_MPV_DIR/mpv.7z" -o"$TMP_MPV_DIR/extracted" >/dev/null

    cp -f "$TMP_MPV_DIR/extracted/mpv.exe" "$DIST_DIR/"
    [ -f "$TMP_MPV_DIR/extracted/mpv.com" ] && cp -f "$TMP_MPV_DIR/extracted/mpv.com" "$DIST_DIR/"
    [ -f "$TMP_MPV_DIR/extracted/d3dcompiler_43.dll" ] && cp -f "$TMP_MPV_DIR/extracted/d3dcompiler_43.dll" "$DIST_DIR/"
    if [ -d "$TMP_MPV_DIR/extracted/mpv" ]; then
        rm -rf "$DIST_DIR/mpv"
        cp -r "$TMP_MPV_DIR/extracted/mpv" "$DIST_DIR/"
    fi
    rm -rf "$TMP_MPV_DIR"
fi


# 3.4 Librería vulkan-1.dll (compatibilidad con VMs y equipos sin drivers Vulkan)
if [ ! -f "$DIST_DIR/vulkan-1.dll" ]; then
    echo "  -> Descargando vulkan-1.dll (MSYS2 MinGW-w64 Vulkan Loader)..."
    TMP_VK_DIR=$(mktemp -d)

    VK_PKG=$(curl -fsSL "https://repo.msys2.org/mingw/mingw64/" \
        | grep -oE 'mingw-w64-x86_64-vulkan-loader-[0-9][^"]+\.pkg\.tar\.zst' \
        | head -n 1 || true)

    if [ -n "$VK_PKG" ] && curl -fL -o "$TMP_VK_DIR/$VK_PKG" "https://repo.msys2.org/mingw/mingw64/$VK_PKG"; then
        tar --zstd -xf "$TMP_VK_DIR/$VK_PKG" -C "$TMP_VK_DIR" "mingw64/bin/vulkan-1.dll" 2>/dev/null || true
        if [ -f "$TMP_VK_DIR/mingw64/bin/vulkan-1.dll" ]; then
            cp -f "$TMP_VK_DIR/mingw64/bin/vulkan-1.dll" "$DIST_DIR/vulkan-1.dll"
            echo "  -> vulkan-1.dll extraído correctamente."
        fi
    else
        echo "  [AVISO] No se pudo descargar vulkan-1.dll automáticamente."
    fi
    rm -rf "$TMP_VK_DIR"
fi

echo "[*] Generando paquete final $ZIP_NAME..."
rm -f "$ZIP_NAME"
(cd "$DIST_DIR" && zip -rq "../$ZIP_NAME" .)

echo "Listo: $ZIP_NAME generado correctamente."