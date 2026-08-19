#!/bin/sh
# 容器启动脚本：编排 Xvfb(虚拟显示) -> x11vnc(画面转发) -> plato-gui(GUI主进程) 三者的
# 启动顺序。Xvfb必须最先起且要等它真正就绪(X11 socket出现)，GUI进程依赖DISPLAY可用；
# x11vnc依赖Xvfb的X11 socket，但和GUI进程谁先谁后影响不大——GUI进程本身不依赖x11vnc，
# 只是x11vnc起晚了用户暂时连不上VNC而已。
set -e

DISPLAY_NUMBER=99
DISPLAY_SOCKET="/tmp/.X11-unix/X${DISPLAY_NUMBER}"
DISPLAY_LOCK="/tmp/.X${DISPLAY_NUMBER}-lock"
XVFB_PID=""
VNC_PID=""
NOVNC_PID=""
FLUXBOX_PID=""

cleanup() {
    for pid in "$FLUXBOX_PID" "$NOVNC_PID" "$VNC_PID" "$XVFB_PID"; do
        if [ -n "$pid" ]; then
            kill "$pid" 2>/dev/null || true
        fi
    done
    rm -f "$DISPLAY_SOCKET" "$DISPLAY_LOCK"
}
trap cleanup EXIT INT TERM

# Fyne需要XDG_RUNTIME_DIR存在且权限为0700，否则会报"XDG_RUNTIME_DIR is invalid"
# (阶段2验证时实测到的告警，虽非致命但清理掉更干净)。
export XDG_RUNTIME_DIR=/tmp/xdg-runtime
mkdir -p "$XDG_RUNTIME_DIR"
chmod 0700 "$XDG_RUNTIME_DIR"

# Fyne会在XDG_CACHE_HOME下创建主题和应用缓存；显式指向可写目录，避免精简镜像中
# /root/.cache不存在时产生启动告警。
export XDG_CACHE_HOME=/tmp/xdg-cache
mkdir -p "$XDG_CACHE_HOME"

rm -f "$DISPLAY_SOCKET" "$DISPLAY_LOCK"
Xvfb ":${DISPLAY_NUMBER}" -screen 0 1280x800x24 &
XVFB_PID=$!

for i in $(seq 1 30); do
	if [ -e "$DISPLAY_SOCKET" ]; then break; fi
    sleep 0.5
done

if [ ! -e "$DISPLAY_SOCKET" ]; then
	echo "Xvfb未在15秒内就绪" >&2
	exit 1
fi

x11vnc -display "$DISPLAY" -forever -shared -nopw -rfbport 5900 &
VNC_PID=$!

# noVNC把浏览器WebSocket转发到本机VNC端口，避免macOS“屏幕共享”与x11vnc的
# 协议兼容问题。Compose仅把6080绑定到宿主机127.0.0.1。
websockify --web=/usr/share/novnc 6081 localhost:5900 &
NOVNC_PID=$!

# fluxbox必须先起来，好友/群组管理面板、各种dialog都是独立的Fyne窗口，没有WM时
# 层叠顺序和焦点管理是错的，实测会被主窗口完全遮挡。
fluxbox -display "$DISPLAY" &
FLUXBOX_PID=$!
sleep 1

/app/plato-gui guiclient --config /app/plato.yaml
