package guiclient

import (
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
)

const (
	webBridgeAddr    = ":6080"
	noVNCBackendURL  = "http://127.0.0.1:6081"
	maxWebInputBytes = 16 * 1024
)

type webBridge struct {
	mu    sync.RWMutex
	state *guiState
	proxy *httputil.ReverseProxy
}

func newWebBridge() *webBridge {
	target, err := url.Parse(noVNCBackendURL)
	if err != nil {
		panic(err)
	}
	return &webBridge{proxy: httputil.NewSingleHostReverseProxy(target)}
}

func (b *webBridge) attach(state *guiState) {
	b.mu.Lock()
	b.state = state
	b.mu.Unlock()
}

func (b *webBridge) serve() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", b.handleHome)
	mux.HandleFunc("/api/input", b.handleInput)
	mux.Handle("/novnc/", http.StripPrefix("/novnc", b.proxy))
	if err := http.ListenAndServe(webBridgeAddr, mux); err != nil {
		log.Printf("GUI web bridge stopped: %v", err)
	}
}

func (b *webBridge) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, webBridgeHTML)
}

func (b *webBridge) handleInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebInputBytes))
	if err != nil {
		http.Error(w, "input too large", http.StatusRequestEntityTooLarge)
		return
	}
	content := string(body)
	if strings.TrimSpace(content) == "" {
		http.Error(w, "input is empty", http.StatusBadRequest)
		return
	}
	b.mu.RLock()
	state := b.state
	b.mu.RUnlock()
	if state == nil {
		http.Error(w, "请先登录Plato IM", http.StatusConflict)
		return
	}
	fyne.DoAndWait(func() { state.appendInput(content) })
	w.WriteHeader(http.StatusNoContent)
}

const webBridgeHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Plato IM</title>
<style>
html,body{height:100%;margin:0;background:#111;color:#eee;font:14px system-ui,sans-serif}
body{display:grid;grid-template-rows:48px 1fr}
header{display:flex;align-items:center;gap:8px;padding:6px 12px;border-bottom:1px solid #333}
input{flex:1;min-width:0;height:34px;box-sizing:border-box;border:1px solid #555;background:#202124;color:#fff;padding:0 10px}
button{height:34px;border:0;background:#2563eb;color:#fff;padding:0 18px;cursor:pointer}
#status{width:96px;color:#aaa}
iframe{width:100%;height:100%;border:0}
</style>
</head>
<body>
<header>
  <input id="text" placeholder="中文输入" autocomplete="off">
  <button id="insert" type="button">插入</button>
  <span id="status"></span>
</header>
<iframe src="/novnc/vnc.html?autoconnect=true&resize=scale&path=novnc/websockify" allow="clipboard-read; clipboard-write"></iframe>
<script>
const input=document.getElementById('text');
const status=document.getElementById('status');
async function insert(){
  if(!input.value.trim())return;
  const response=await fetch('/api/input',{method:'POST',headers:{'Content-Type':'text/plain;charset=UTF-8'},body:input.value});
  if(response.ok){input.value='';status.textContent='已插入';}
  else{status.textContent=await response.text();}
}
document.getElementById('insert').addEventListener('click',insert);
input.addEventListener('keydown',event=>{if(event.key==='Enter'&&!event.isComposing){event.preventDefault();insert();}});
</script>
</body>
</html>`
