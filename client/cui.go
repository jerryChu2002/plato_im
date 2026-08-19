package client

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gookit/color"
	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/convid"
	"github.com/hardcore-os/plato/common/sdk"
	"github.com/rocket049/gocui"
)

func init() {
	rand.Seed(time.Now().UnixNano())
	verbose = true
	step = 1
}

var (
	buf      string
	chat     *sdk.Chat
	step     int
	verbose  bool
	myDid    uint64
	myUserID uint64
)

// currentTarget 记录当前"正在聊天"的对象：要么是某个did(单聊)，要么是某个群(群聊)，
// 通过 /chat 、/group 命令切换。纯文本输入(不以/开头)会发给当前target。
var currentTarget = struct {
	isGroup bool
	id      uint64
}{}

const helpText = `可用命令:
/help                       查看命令列表
/conversations               查看会话列表(好友+群聊，点开即可用/chat或/group切换过去聊天)
	/chat <userID>              切换到与某个账号用户的单聊
/group <groupID>            切换到某个群的群聊
/addfriend <did> [附言]      发送好友申请
/requests                   查看收到的待处理好友申请
/sentrequests                查看我发出的好友申请及处理状态
/accept <requestID>         同意好友申请
/reject <requestID>         拒绝好友申请
/friends                    查看好友列表
/delfriend <did>            删除好友
/creategroup <群名>          创建群聊
/invite <groupID> <did>     邀请某人入群
/mygroups                   查看我加入的群
/members <groupID>          查看群成员
直接输入文字并回车            发送消息给当前聊天对象(先用/chat或/group选择)`

func setHeadText(g *gocui.Gui, msg string) {
	v, err := g.View("head")
	if err == nil {
		v.Clear()
		fmt.Fprint(v, color.FgGreen.Text(msg))
	}
}

type VOT struct {
	Name, Msg, Sep string
}

func (self VOT) Show(g *gocui.Gui) error {
	v, err := g.View("out")
	if err != nil {
		return nil
	}
	fmt.Fprintf(v, "%v:%v%v\n", color.FgGreen.Text(self.Name), self.Sep,
		color.FgYellow.Text(self.Msg))
	return nil
}
func viewPrint(g *gocui.Gui, name, msg string, newline bool) {
	var out VOT
	out.Name = name
	out.Msg = msg
	if newline {
		out.Sep = "\n"
	} else {
		out.Sep = " "
	}
	g.Update(out.Show)
}

// doRecv work in goroutine
func doRecv(g *gocui.Gui) {
	recvChannel := chat.Recv()
	for msg := range recvChannel {
		if msg != nil {
			switch msg.Type {
			case sdk.MsgTypeText:
				viewPrint(g, msg.Name, msg.Content, false)
			case sdk.MsgTypeSystem:
				viewPrint(g, "system", msg.Content, true)
			case sdk.MsgTypeAck:
				//TODO 默认不处理
			}
		}
	}
}

func quit(g *gocui.Gui, v *gocui.View) error {
	chat.Close()
	ov, _ := g.View("out")
	buf = ov.Buffer()
	g.Close()
	return gocui.ErrQuit
}

func doSay(g *gocui.Gui, cv *gocui.View) {
	v, err := g.View("out")
	if cv == nil || err != nil {
		return
	}
	p := cv.ReadEditor()
	if p == nil {
		v.Autoscroll = true
		return
	}
	text := strings.TrimSpace(string(p))
	if text == "" {
		v.Autoscroll = true
		return
	}
	if strings.HasPrefix(text, "/") {
		handleCommand(g, text)
		v.Autoscroll = true
		return
	}
	if currentTarget.id == 0 {
		viewPrint(g, "system", "还没有选择聊天对象，先用 /chat <did> 或 /group <groupID> 选择", false)
		v.Autoscroll = true
		return
	}
	msg := &sdk.Message{
		Type:       sdk.MsgTypeText,
		Name:       fmt.Sprintf("%d", myUserID),
		FormUserID: fmt.Sprintf("%d", myUserID),
		Content:    text,
	}
	idKey := fmt.Sprintf("%d", chat.GetCurClientID())
	viewPrint(g, "me:"+idKey, text, false)
	if currentTarget.isGroup {
		chat.Send(msg, 0, currentTarget.id)
	} else {
		chat.Send(msg, currentTarget.id, 0)
	}
	v.Autoscroll = true
}

// handleCommand 解析并执行以"/"开头的命令：好友/群管理走domain/friend、domain/group的
// 直接rpc调用(client/social.go)，收发消息走common/sdk的TCP长连接。
func handleCommand(g *gocui.Gui, text string) {
	parts := strings.Fields(text)
	switch parts[0] {
	case "/help":
		viewPrint(g, "system", helpText, true)

	case "/conversations":
		showConversations(g)

	case "/chat":
		if len(parts) < 2 {
			viewPrint(g, "system", "用法: /chat <userID>", false)
			return
		}
		did, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			viewPrint(g, "system", "userID格式不对", false)
			return
		}
		currentTarget.isGroup = false
		currentTarget.id = did
		go chat.OpenConversation(convid.Single(myUserID, did)) // 此处did变量承载目标userID，保留变量名避免扩大改动
		viewPrint(g, "system", fmt.Sprintf("已切换到与 userID=%d 的单聊", did), false)

	case "/group":
		if len(parts) < 2 {
			viewPrint(g, "system", "用法: /group <groupID>", false)
			return
		}
		gid, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			viewPrint(g, "system", "groupID格式不对", false)
			return
		}
		currentTarget.isGroup = true
		currentTarget.id = gid
		go chat.OpenConversation(convid.Group(gid)) // 补拉离线消息+上报已读进度
		viewPrint(g, "system", fmt.Sprintf("已切换到群 %d 的聊天", gid), false)

	case "/addfriend":
		if len(parts) < 2 {
			viewPrint(g, "system", "用法: /addfriend <did> [附言]", false)
			return
		}
		did, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			viewPrint(g, "system", "did格式不对", false)
			return
		}
		msg := ""
		if len(parts) > 2 {
			msg = strings.Join(parts[2:], " ")
		}
		reqID, err := addFriend(myDid, did, msg)
		if err != nil {
			viewPrint(g, "system", "申请失败: "+err.Error(), false)
			return
		}
		viewPrint(g, "system", fmt.Sprintf("已发送好友申请，requestID=%d", reqID), false)

	case "/accept":
		handleReqCmd(g, parts, true)
	case "/reject":
		handleReqCmd(g, parts, false)

	case "/friends":
		friends, err := listFriends(myDid)
		if err != nil {
			viewPrint(g, "system", "查询失败: "+err.Error(), false)
			return
		}
		if len(friends) == 0 {
			viewPrint(g, "system", "暂无好友", false)
			return
		}
		for _, f := range friends {
			viewPrint(g, "好友", fmt.Sprintf("did=%d 备注=%s", f.Did, f.Remark), true)
		}

	case "/delfriend":
		if len(parts) < 2 {
			viewPrint(g, "system", "用法: /delfriend <did>", false)
			return
		}
		did, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			viewPrint(g, "system", "did格式不对", false)
			return
		}
		if err := deleteFriend(myDid, did); err != nil {
			viewPrint(g, "system", "删除失败: "+err.Error(), false)
			return
		}
		viewPrint(g, "system", fmt.Sprintf("已删除好友 did=%d", did), false)

	case "/requests":
		reqs, err := listPendingRequests(myDid)
		if err != nil {
			viewPrint(g, "system", "查询失败: "+err.Error(), false)
			return
		}
		if len(reqs) == 0 {
			viewPrint(g, "system", "没有待处理的好友申请", false)
			return
		}
		for _, r := range reqs {
			viewPrint(g, "好友申请", fmt.Sprintf("requestID=%d from did=%d 附言:%s", r.ID, r.FromDid, r.Message), true)
		}

	case "/sentrequests":
		reqs, err := listSentRequests(myDid)
		if err != nil {
			viewPrint(g, "system", "查询失败: "+err.Error(), false)
			return
		}
		if len(reqs) == 0 {
			viewPrint(g, "system", "还没有发出过好友申请", false)
			return
		}
		for _, r := range reqs {
			viewPrint(g, "我发出的申请", fmt.Sprintf("requestID=%d to did=%d 状态:%s", r.ID, r.ToDid, r.Status.String()), true)
		}

	case "/creategroup":
		if len(parts) < 2 {
			viewPrint(g, "system", "用法: /creategroup <群名>", false)
			return
		}
		name := strings.Join(parts[1:], " ")
		gid, err := createGroup(myUserID, name)
		if err != nil {
			viewPrint(g, "system", "建群失败: "+err.Error(), false)
			return
		}
		viewPrint(g, "system", fmt.Sprintf("建群成功，groupID=%d", gid), false)

	case "/invite":
		if len(parts) < 3 {
			viewPrint(g, "system", "用法: /invite <groupID> <userID>", false)
			return
		}
		gid, err1 := strconv.ParseUint(parts[1], 10, 64)
		memberUserID, err2 := strconv.ParseUint(parts[2], 10, 64)
		if err1 != nil || err2 != nil {
			viewPrint(g, "system", "参数格式不对", false)
			return
		}
		if err := inviteToGroup(gid, myUserID, memberUserID); err != nil {
			viewPrint(g, "system", "邀请失败: "+err.Error(), false)
			return
		}
		viewPrint(g, "system", fmt.Sprintf("已邀请 userID=%d 加入群 %d", memberUserID, gid), false)

	case "/mygroups":
		groups, err := listMyGroups(myUserID)
		if err != nil {
			viewPrint(g, "system", "查询失败: "+err.Error(), false)
			return
		}
		if len(groups) == 0 {
			viewPrint(g, "system", "还没有加入任何群", false)
			return
		}
		for _, gr := range groups {
			viewPrint(g, "我的群", fmt.Sprintf("groupID=%d 名称=%s 成员数=%d", gr.GroupID, gr.Name, gr.MemberCount), true)
		}

	case "/members":
		if len(parts) < 2 {
			viewPrint(g, "system", "用法: /members <groupID>", false)
			return
		}
		gid, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			viewPrint(g, "system", "groupID格式不对", false)
			return
		}
		members, err := listGroupMembers(gid)
		if err != nil {
			viewPrint(g, "system", "查询失败: "+err.Error(), false)
			return
		}
		for _, m := range members {
			viewPrint(g, "群成员", fmt.Sprintf("userID=%d role=%d", m.MemberUserID, m.Role), true)
		}

	default:
		viewPrint(g, "system", "未知命令，输入 /help 查看命令列表", false)
	}
}

// showConversations 把好友列表和我加入的群合并展示成"会话列表"：每个好友、每个群
// 都是一个可以直接切换过去聊天的会话，点开对应命令(/chat或/group)即可开始收发消息。
func showConversations(g *gocui.Gui) {
	friends, err := listFriends(myDid)
	if err != nil {
		viewPrint(g, "system", "查询好友会话失败: "+err.Error(), false)
		return
	}
	groups, err := listMyGroups(myUserID)
	if err != nil {
		viewPrint(g, "system", "查询群聊会话失败: "+err.Error(), false)
		return
	}
	if len(friends) == 0 && len(groups) == 0 {
		viewPrint(g, "system", "还没有任何会话，先用 /addfriend 加好友或 /creategroup 建群", false)
		return
	}
	for _, f := range friends {
		unread := chat.UnreadCount(convid.Single(myDid, f.Did))
		viewPrint(g, "会话[单聊]", fmt.Sprintf("did=%d 备注=%s 未读=%d  -> 用 /chat %d 进入", f.Did, f.Remark, unread, f.Did), true)
	}
	for _, gr := range groups {
		unread := chat.UnreadCount(convid.Group(gr.GroupID))
		viewPrint(g, "会话[群聊]", fmt.Sprintf("groupID=%d 名称=%s 成员数=%d 未读=%d  -> 用 /group %d 进入", gr.GroupID, gr.Name, gr.MemberCount, unread, gr.GroupID), true)
	}
}

func handleReqCmd(g *gocui.Gui, parts []string, agree bool) {
	if len(parts) < 2 {
		viewPrint(g, "system", "用法: /accept <requestID> 或 /reject <requestID>", false)
		return
	}
	reqID, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		viewPrint(g, "system", "requestID格式不对", false)
		return
	}
	if err := handleFriendRequest(reqID, myDid, agree); err != nil {
		viewPrint(g, "system", "处理失败: "+err.Error(), false)
		return
	}
	action := "同意"
	if !agree {
		action = "拒绝"
	}
	viewPrint(g, "system", fmt.Sprintf("已%s好友申请 requestID=%d", action, reqID), false)
}

func viewUpdate(g *gocui.Gui, cv *gocui.View) error {
	doSay(g, cv)
	l := len(cv.Buffer())
	cv.MoveCursor(0-l, 0, true)
	cv.Clear()
	return nil
}

func viewUpScroll(g *gocui.Gui, cv *gocui.View) error {
	v, err := g.View("out")
	v.Autoscroll = false
	ox, oy := v.Origin()
	if err == nil {
		v.SetOrigin(ox, oy-1)
	}
	return nil
}

func viewDownScroll(g *gocui.Gui, cv *gocui.View) error {
	v, err := g.View("out")
	_, y := v.Size()
	ox, oy := v.Origin()
	lnum := len(v.BufferLines())
	if err == nil {
		if oy > lnum-y-1 {
			v.Autoscroll = true
		} else {
			v.SetOrigin(ox, oy+1)
		}
	}
	return nil
}

func viewOutput(g *gocui.Gui, x0, y0, x1, y1 int) error {
	v, err := g.SetView("out", x0, y0, x1, y1)
	if err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Wrap = true
		v.Overwrite = false
		v.Autoscroll = true
		v.SelBgColor = gocui.ColorRed
		v.Title = "Messages"
	}
	return nil
}
func viewInput(g *gocui.Gui, x0, y0, x1, y1 int) error {
	if v, err := g.SetView("main", x0, y0, x1, y1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		//当 err == gocui.ErrUnknownView 时运行
		v.Editable = true
		v.Wrap = true
		v.Overwrite = false
		if _, err := g.SetCurrentView("main"); err != nil {
			return err
		}
	}
	return nil
}

func viewHead(g *gocui.Gui, x0, y0, x1, y1 int) error {
	if v, err := g.SetView("head", x0, y0, x1, y1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Wrap = false
		v.Overwrite = true
		msg := fmt.Sprintf("plato im | user=%d | did=%d | 输入 /help 查看命令", myUserID, myDid)
		setHeadText(g, msg)
	}
	return nil
}
func layout(g *gocui.Gui) error {
	maxX, maxY := g.Size()
	if err := viewHead(g, 1, 1, maxX-1, 3); err != nil {
		return err
	}
	if err := viewOutput(g, 1, 4, maxX-1, maxY-4); err != nil {
		return err
	}
	if err := viewInput(g, 1, maxY-3, maxX-1, maxY-1); err != nil {
		return err
	}
	return nil
}

var pos int

func pasteUP(g *gocui.Gui, cv *gocui.View) error {
	v, err := g.View("out")
	if err != nil {
		fmt.Fprintf(cv, "error:%s", err)
		return nil
	}
	bls := v.BufferLines()
	lnum := len(bls)
	if pos < lnum-1 {
		pos++
	}
	cv.Clear()
	fmt.Fprintf(cv, "%s", bls[lnum-pos-1])
	return nil
}

func pasteDown(g *gocui.Gui, cv *gocui.View) error {
	v, err := g.View("out")
	if err != nil {
		fmt.Fprintf(cv, "error:%s", err)
		return nil
	}
	if pos > 0 {
		pos--
	}
	bls := v.BufferLines()
	lnum := len(bls)
	cv.Clear()
	fmt.Fprintf(cv, "%s", bls[lnum-pos-1])
	return nil
}

// promptLogin 在进入gocui全屏模式之前读取账号、密码和当前设备did。
// gocui会接管终端的原始输入模式，所以这一步必须在NewGui之前完成。
func promptLogin() (*sdk.LoginIdentity, uint64) {
	var account, password, input string
	fmt.Print("账号: ")
	fmt.Scanln(&account)
	fmt.Print("密码: ")
	fmt.Scanln(&password)
	fmt.Print("设备did(非0数字): ")
	fmt.Scanln(&input)
	did, err := strconv.ParseUint(strings.TrimSpace(input), 10, 64)
	if err != nil || did == 0 {
		log.Panic("did必须是非0数字")
	}
	identity, err := sdk.LoginAccount(account, password, did)
	if err != nil {
		log.Panicf("登录失败: %v", err)
	}
	return identity, did
}

func RunMain(configPath string) {
	// step0 加载配置、初始化好友/群聊的rpc client
	config.Init(configPath)
	initSocialClients()

	// step1 登录：从终端读取did
	identity, did := promptLogin()
	myDid, myUserID = did, identity.UserID

	// step2 创建chat的核心对象，用did登录。
	// gateway.service_addr在配置里既可能是IP(本地跑)也可能是docker容器名(容器化跑)，
	// 统一用net.LookupIP解析成真正可拨号的IP——LookupIP对字面量IP直接原样返回，
	// 对容器名则走Docker内嵌DNS解析，两种场景都能处理。
	gatewayHost := config.GetGatewayServiceAddr()
	ips, err := net.LookupIP(gatewayHost)
	if err != nil || len(ips) == 0 {
		log.Panicf("resolve gateway host %q failed: %v", gatewayHost, err)
	}
	chat = sdk.NewChat(ips[0], config.GetGatewayTCPServerPort(), myUserID, myDid, identity.SessionToken,
		"user", fmt.Sprintf("sess-%d", myDid), syncAllConversations)

	// step3 创建 GUI 图层对象并进行参与与回调函数的配置
	g, err := gocui.NewGui(gocui.OutputNormal)
	if err != nil {
		log.Panicln(err)
	}

	g.Cursor = true
	g.Mouse = false
	g.ASCII = false
	// 设置编排函数
	g.SetManagerFunc(layout)

	// 注册回调事件
	if err := g.SetKeybinding("main", gocui.KeyCtrlC, gocui.ModNone, quit); err != nil {
		log.Panicln(err)
	}

	//发送消息入口
	if err := g.SetKeybinding("main", gocui.KeyEnter, gocui.ModNone, viewUpdate); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("main", gocui.KeyPgup, gocui.ModNone, viewUpScroll); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("main", gocui.KeyPgdn, gocui.ModNone, viewDownScroll); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("main", gocui.KeyArrowDown, gocui.ModNone, pasteDown); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("main", gocui.KeyArrowUp, gocui.ModNone, pasteUP); err != nil {
		log.Panicln(err)
	}
	// 启动消费函数
	go doRecv(g)
	if err := g.MainLoop(); err != nil {
		log.Println(err)
	}
	os.WriteFile("chat.log", []byte(buf), 0644)
}
