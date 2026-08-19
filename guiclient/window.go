package guiclient

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/hardcore-os/plato/common/config"
	frienddto "github.com/hardcore-os/plato/common/idl/domain/friend"
	groupdto "github.com/hardcore-os/plato/common/idl/domain/group"
	"github.com/hardcore-os/plato/common/sdk"
	"github.com/hardcore-os/plato/common/socialsvc"
)

// convEntry 是左侧会话列表的一行数据，好友和群统一成同一种展示形态。
type convEntry struct {
	isGroup        bool
	did            uint64 // isGroup=false时保存目标账号userID
	groupID        uint64 // isGroup=true时有效
	title          string // 展示名：好友备注/did，或群名
	conversationID string
	unread         uint64
}

type chatLine struct {
	name    string
	content string
	system  bool
	seq     uint64 // 服务端分配的会话内序号；0表示尚未取得服务端序号的本地消息
}

// guiState 持有整个GUI运行期的共享状态，对应cui.go里那些包级全局变量
// (chat/myDid/currentTarget)，只是这里显式聚成一个struct，避免包级全局到处飞。
type guiState struct {
	app      fyne.App
	win      fyne.Window
	chat     *sdk.Chat
	myDid    uint64
	myUserID uint64

	mu            sync.Mutex
	conversations []convEntry
	currentConv   *convEntry // 当前打开的会话，nil表示还没选

	convList   *widget.List
	chatTitle  *widget.Label
	msgArea    *widget.RichText
	msgScroll  *container.Scroll
	inputBox   *widget.Entry
	sendBtn    *widget.Button
	status     *widget.Label
	uiReady    chan struct{}
	messages   map[string][]chatLine
	onLogout   func()    // 退出当前账号后重新展示登录窗口
	logoutOnce sync.Once // 防止退出按钮和窗口关闭事件重复执行退出流程

	activeGroupMembers []*groupdto.GroupMemberDTO // 当前群会话的成员列表
	groupMemberList    *widget.List               // 群成员右侧列表
	groupMemberPanel   fyne.CanvasObject          // 仅在打开群会话时显示的右侧面板

	// 下面几个cached*字段是好友/群管理面板列表控件的数据源缓存：Fyne的widget.List
	// 通过闭包捕获回调函数读取数据，而不是每次重建整个list，所以需要一个地方持有
	// "最近一次rpc查询到的数据"，reload*函数负责刷新它们再触发list.Refresh()。
	cachedFriendsForTab  []*frienddto.FriendDTO
	cachedPendingForTab  []*frienddto.FriendRequestDTO
	cachedSentForTab     []*frienddto.FriendRequestDTO
	cachedMyGroupsForTab []*groupdto.GroupInfoDTO
	cachedMembersForTab  []*groupdto.GroupMemberDTO
}

// RunMain 是guiclient的入口，对应client/cui.go的RunMain：加载配置、初始化好友/群rpc
// client、用一个登录窗口收集did、登录成功后切到主窗口、进入Fyne事件循环。
// 全程只用一个fyne.App实例：GUI程序底层的图形驱动(GLFW)按进程只初始化一次，
// 先后起两个app.New()在同一进程里跑事件循环并不是Fyne支持的用法。
func RunMain(configPath string) {
	config.Init(configPath)

	a := app.NewWithID("io.github.hardcore-os.plato.guiclient")
	bridge := newWebBridge()
	go bridge.serve()
	initErr := socialsvc.InitClients()
	var showLogin func()
	showLogin = func() {
		showLoginWindow(a, func(identity *sdk.LoginIdentity, myDid uint64, gatewayHost string, gatewayPort int) error {
			if initErr != nil {
				return fmt.Errorf("初始化好友/群服务失败: %w", initErr)
			}
			return startMainWindow(a, bridge, identity, myDid, gatewayHost, gatewayPort, func() {
				bridge.attach(nil)
				showLogin()
			})
		})
	}
	showLogin()
	a.Run()
}

// showLoginWindow 展示一个简单的登录窗口收集did，onLogin在用户提交后触发。
// 对应cui.go的promptDid()，只是GUI程序没有交互式终端可用，改成窗口输入。
func showLoginWindow(a fyne.App, onLogin func(identity *sdk.LoginIdentity, myDid uint64, gatewayHost string, gatewayPort int) error) {
	w := a.NewWindow("plato im - 登录")
	w.Resize(fyne.NewSize(440, 360))

	accountEntry := widget.NewEntry()
	accountEntry.SetPlaceHolder("输入账号")
	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("至少6位密码")
	nicknameEntry := widget.NewEntry()
	nicknameEntry.SetPlaceHolder("注册时使用，登录可留空")
	didEntry := widget.NewEntry()
	didEntry.SetPlaceHolder("当前设备did(非0数字)")
	gatewayEntry := widget.NewEntry()
	gatewayEntry.SetText(config.GetGatewayServiceAddr())
	portEntry := widget.NewEntry()
	portEntry.SetText(strconv.Itoa(config.GetGatewayTCPServerPort()))

	errLabel := widget.NewLabel("")
	statusLabel := widget.NewLabel("")
	loginBtn := widget.NewButtonWithIcon("登录", theme.LoginIcon(), nil)
	registerBtn := widget.NewButtonWithIcon("注册并登录", theme.ContentAddIcon(), nil)
	submit := func(register bool) {
		account := strings.TrimSpace(accountEntry.Text)
		password := passwordEntry.Text
		if account == "" || len(password) < 6 {
			errLabel.SetText("请输入账号和至少6位密码")
			return
		}
		did, err := strconv.ParseUint(strings.TrimSpace(didEntry.Text), 10, 64)
		if err != nil || did == 0 {
			errLabel.SetText("did必须是非0的正整数")
			return
		}
		gatewayHost := strings.TrimSpace(gatewayEntry.Text)
		gatewayPort, portErr := strconv.Atoi(strings.TrimSpace(portEntry.Text))
		if gatewayHost == "" || portErr != nil || gatewayPort < 1 || gatewayPort > 65535 {
			errLabel.SetText("gateway地址或端口无效")
			return
		}
		errLabel.SetText("")
		statusLabel.SetText("正在连接 gateway...")
		loginBtn.Disable()
		registerBtn.Disable()
		go func() {
			var identity *sdk.LoginIdentity
			var authErr error
			if register {
				identity, authErr = sdk.RegisterAccount(account, password, strings.TrimSpace(nicknameEntry.Text), did)
			} else {
				identity, authErr = sdk.LoginAccount(account, password, did)
			}
			if authErr == nil {
				authErr = onLogin(identity, did, gatewayHost, gatewayPort)
			}
			fyne.Do(func() {
				if authErr != nil {
					errLabel.SetText(authErr.Error())
					statusLabel.SetText("")
					loginBtn.Enable()
					registerBtn.Enable()
					return
				}
				w.Close()
			})
		}()
	}
	loginBtn.OnTapped = func() { submit(false) }
	registerBtn.OnTapped = func() { submit(true) }
	didEntry.OnSubmitted = func(string) { submit(false) }

	form := widget.NewForm(
		widget.NewFormItem("账号", accountEntry),
		widget.NewFormItem("密码", passwordEntry),
		widget.NewFormItem("昵称", nicknameEntry),
		widget.NewFormItem("did", didEntry),
		widget.NewFormItem("Gateway", gatewayEntry),
		widget.NewFormItem("端口", portEntry),
	)
	w.SetContent(container.NewVBox(
		widget.NewLabel("Plato IM"),
		form,
		loginBtn,
		registerBtn,
		statusLabel,
		errLabel,
	))
	w.Show()
}

// startMainWindow 建立TCP长连接并登录、装配主窗口界面、进入收消息循环。
func startMainWindow(a fyne.App, bridge *webBridge, identity *sdk.LoginIdentity, myDid uint64, gatewayHost string, gatewayPort int, onLogout func()) error {
	gs := &guiState{
		app:      a,
		myDid:    myDid,
		myUserID: identity.UserID,
		uiReady:  make(chan struct{}),
		messages: make(map[string][]chatLine),
		onLogout: onLogout,
	}
	ips, err := net.LookupIP(gatewayHost)
	if err != nil {
		return fmt.Errorf("解析 gateway %q 失败: %w", gatewayHost, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("解析 gateway %q 失败: 没有可用IP", gatewayHost)
	}
	selectedIP := ips[int(myDid%uint64(len(ips)))]
	gs.chat, err = sdk.DialChat(selectedIP, gatewayPort, identity.UserID, myDid, identity.SessionToken,
		"user", fmt.Sprintf("sess-%d", myDid),
		func() { gs.onReady() })
	if err != nil {
		return err
	}

	fyne.DoAndWait(func() {
		gs.win = a.NewWindow(fmt.Sprintf("plato im | user=%d | did=%d", identity.UserID, myDid))
		gs.buildUI()
		close(gs.uiReady)
		gs.chat.SetConnectionStateHandler(gs.setConnectionState)
		gs.win.SetCloseIntercept(gs.logout)
		gs.win.Resize(fyne.NewSize(900, 600))
		gs.win.Show()
		bridge.attach(gs)
	})
	go gs.recvLoop()
	return nil
}

func (gs *guiState) logout() {
	gs.logoutOnce.Do(func() {
		gs.chat.Close()
		if gs.onLogout != nil {
			gs.onLogout()
		}
		gs.win.Close()
	})
}
