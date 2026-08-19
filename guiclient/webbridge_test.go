package guiclient

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestWebBridgeHome(t *testing.T) {
	bridge := newWebBridge()
	recorder := httptest.NewRecorder()
	bridge.handleHome(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), "中文输入") {
		t.Fatal("home page does not contain the Chinese input control")
	}
	if !strings.Contains(recorder.Body.String(), "path=novnc/websockify") {
		t.Fatal("home page does not configure the proxied noVNC websocket path")
	}
}

func TestWebBridgeInputRequiresLogin(t *testing.T) {
	bridge := newWebBridge()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/input", strings.NewReader("你好"))
	bridge.handleInput(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
}

func TestWebBridgeInputAppendsUTF8(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	bridge := newWebBridge()
	state := &guiState{app: app, inputBox: widget.NewMultiLineEntry()}
	bridge.attach(state)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/input", strings.NewReader("你好，Plato"))
	bridge.handleInput(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if state.inputBox.Text != "你好，Plato" {
		t.Fatalf("input = %q, want %q", state.inputBox.Text, "你好，Plato")
	}
}
