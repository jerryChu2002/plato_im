package guiclient

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/hardcore-os/plato/common/convid"
	"github.com/hardcore-os/plato/common/socialsvc"
)

// buildUI 装配主窗口：顶部工具条(好友/群管理入口) + 左侧会话列表 + 右侧聊天区。
// 对应cui.go的layout()，只是这里用Fyne原生控件替代gocui的手工分区绘制。
func (gs *guiState) buildUI() {
	refreshBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() { gs.refreshConversations() })
	refreshBtn.Importance = widget.LowImportance
	friendBtn := widget.NewButton("好友", func() { gs.showFriendPanel() })
	groupBtn := widget.NewButton("群组", func() { gs.showGroupPanel() })
	directBtn := widget.NewButtonWithIcon("单聊", theme.MailComposeIcon(), func() { gs.showDirectChatDialog() })
	gs.status = widget.NewLabel("正在登录")
	reconnectBtn := widget.NewButtonWithIcon("重连", theme.ViewRefreshIcon(), func() { gs.chat.ReConn() })
	logoutBtn := widget.NewButtonWithIcon("退出", theme.LogoutIcon(), gs.logout)
	topBar := container.NewHBox(
		widget.NewLabel(fmt.Sprintf("user=%d did=%d", gs.myUserID, gs.myDid)),
		directBtn, friendBtn, groupBtn, gs.status, reconnectBtn, logoutBtn,
	)

	gs.convList = widget.NewList(
		func() int {
			gs.mu.Lock()
			defer gs.mu.Unlock()
			return len(gs.conversations)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			gs.mu.Lock()
			defer gs.mu.Unlock()
			if i < 0 || i >= len(gs.conversations) {
				return
			}
			e := gs.conversations[i]
			label := o.(*widget.Label)
			if e.unread > 0 {
				label.SetText(fmt.Sprintf("%s (%d)", e.title, e.unread))
			} else {
				label.SetText(e.title)
			}
		},
	)
	gs.convList.OnSelected = func(i widget.ListItemID) {
		gs.mu.Lock()
		if i < 0 || i >= len(gs.conversations) {
			gs.mu.Unlock()
			return
		}
		entry := gs.conversations[i]
		gs.mu.Unlock()
		gs.openConversation(&entry)
	}

	left := container.NewBorder(refreshBtn, nil, nil, nil, gs.convList)

	gs.chatTitle = widget.NewLabel("请选择左侧会话开始聊天")
	gs.msgArea = widget.NewRichText()
	gs.msgArea.Wrapping = fyne.TextWrapWord
	gs.msgScroll = container.NewVScroll(gs.msgArea)

	gs.inputBox = widget.NewMultiLineEntry()
	gs.inputBox.SetPlaceHolder("输入消息，点击发送")
	gs.sendBtn = widget.NewButtonWithIcon("发送", theme.MailSendIcon(), gs.sendCurrentInput)
	inputRow := container.NewBorder(nil, nil, nil, gs.sendBtn, gs.inputBox)

	chatPanel := container.NewBorder(gs.chatTitle, inputRow, nil, nil, gs.msgScroll)
	gs.groupMemberList = widget.NewList(
		func() int {
			gs.mu.Lock()
			defer gs.mu.Unlock()
			return len(gs.activeGroupMembers)
		},
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, object fyne.CanvasObject) {
			gs.mu.Lock()
			defer gs.mu.Unlock()
			if i < 0 || i >= len(gs.activeGroupMembers) {
				return
			}
			member := gs.activeGroupMembers[i]
			role := "成员"
			if member.GetRole() == 1 {
				role = "群主"
			}
			object.(*widget.Label).SetText(fmt.Sprintf("user=%d  %s", member.GetMemberUserID(), role))
		},
	)
	gs.groupMemberPanel = container.NewBorder(widget.NewLabel("群成员"), nil, nil, nil, gs.groupMemberList)
	gs.groupMemberPanel.Hide()
	chatAndMembers := container.NewHSplit(chatPanel, gs.groupMemberPanel)
	chatAndMembers.Offset = 0.78

	split := container.NewHSplit(left, chatAndMembers)
	split.Offset = 0.28

	gs.win.SetContent(container.NewBorder(topBar, nil, nil, nil, split))

}

// onReady 是Chat.OnReady的实现：登录/重连成功后，补拉离线消息 + 刷新一次会话列表。
func (gs *guiState) onReady() {
	<-gs.uiReady
	socialsvc.SyncAllConversations(gs.chat, gs.myUserID)
	fyne.Do(func() { gs.refreshConversations() })
}

func (gs *guiState) setConnectionState(state string) {
	fyne.Do(func() {
		gs.status.SetText(state)
		if state == "已连接" {
			gs.inputBox.Enable()
			gs.sendBtn.Enable()
			return
		}
		gs.inputBox.Disable()
		gs.sendBtn.Disable()
	})
}

func (gs *guiState) showDirectChatDialog() {
	userEntry := widget.NewEntry()
	userEntry.SetPlaceHolder("对方userID")
	dialog.ShowForm("发起单聊", "打开", "取消",
		[]*widget.FormItem{widget.NewFormItem("userID", userEntry)},
		func(ok bool) {
			if !ok {
				return
			}
			var targetUserID uint64
			if _, err := fmt.Sscanf(userEntry.Text, "%d", &targetUserID); err != nil || targetUserID == 0 || targetUserID == gs.myUserID {
				dialog.ShowError(fmt.Errorf("请输入有效且不同于自己的userID"), gs.win)
				return
			}
			gs.openOrCreateDirectConversation(targetUserID)
		}, gs.win)
}

func (gs *guiState) openOrCreateDirectConversation(targetUserID uint64) {
	conversationID := convid.Single(gs.myUserID, targetUserID)
	gs.mu.Lock()
	for i := range gs.conversations {
		if gs.conversations[i].conversationID == conversationID {
			entry := gs.conversations[i]
			gs.mu.Unlock()
			gs.convList.Select(widget.ListItemID(i))
			gs.openConversation(&entry)
			return
		}
	}
	gs.conversations = append([]convEntry{{
		did:            targetUserID,
		title:          fmt.Sprintf("user=%d", targetUserID),
		conversationID: conversationID,
	}}, gs.conversations...)
	entry := gs.conversations[0]
	gs.mu.Unlock()
	gs.convList.Refresh()
	gs.convList.Select(0)
	gs.openConversation(&entry)
}

// refreshConversations 重新拉取好友+群列表，合并成会话列表，异步查每个会话的未读数。
// 对应cui.go的showConversations()。
func (gs *guiState) refreshConversations() {
	accountConversations := gs.chat.SyncUserConversations()
	groups, err := socialsvc.ListMyGroups(gs.myUserID)
	if err != nil {
		groups = nil
	}

	entries := make([]convEntry, 0, len(accountConversations)+len(groups))
	for _, conversation := range accountConversations {
		lastMsg := conversation.GetLastMsg()
		targetUserID := lastMsg.GetFromUserID()
		if targetUserID == gs.myUserID {
			targetUserID = lastMsg.GetToUserID()
		}
		entries = append(entries, convEntry{
			did:            targetUserID,
			title:          fmt.Sprintf("user=%d", targetUserID),
			conversationID: conversation.GetConversationID(),
			unread:         conversation.GetUnreadCount(),
		})
	}
	for _, g := range groups {
		entries = append(entries, convEntry{
			isGroup:        true,
			groupID:        g.GetGroupID(),
			title:          fmt.Sprintf("[群]%s(%d人)", g.GetName(), g.GetMemberCount()),
			conversationID: convid.Group(g.GetGroupID()),
		})
	}

	gs.mu.Lock()
	existing := make(map[string]convEntry, len(gs.conversations))
	for _, entry := range gs.conversations {
		existing[entry.conversationID] = entry
	}
	seen := make(map[string]struct{}, len(entries))
	for i := range entries {
		seen[entries[i].conversationID] = struct{}{}
		if previous, ok := existing[entries[i].conversationID]; ok {
			entries[i].unread = previous.unread
		}
	}
	// 直接按did发起的会话不一定是好友关系，刷新好友/群列表时仍需保留。
	for _, previous := range gs.conversations {
		if _, ok := seen[previous.conversationID]; !ok {
			entries = append(entries, previous)
		}
	}
	gs.conversations = entries
	gs.mu.Unlock()
	gs.convList.Refresh()

	// 未读数是逐个查询的rpc调用，放到后台goroutine做，避免阻塞UI；查到后
	// 用fyne.Do把结果写回UI线程再刷新列表。
	go func() {
		for idx := range entries {
			unread := gs.chat.UnreadCount(entries[idx].conversationID)
			fyne.Do(func() {
				gs.mu.Lock()
				for i := range gs.conversations {
					if gs.conversations[i].conversationID == entries[idx].conversationID {
						gs.conversations[i].unread = unread
						break
					}
				}
				gs.mu.Unlock()
				gs.convList.Refresh()
			})
		}
	}()
}

// openConversation 是"点开某个会话"的完整实现：设置当前会话、清空聊天区、
// 补拉该会话已读进度之后的消息并上报已读(OpenConversation)、清零该会话未读角标。
// 对应cui.go的/chat和/group命令。
func (gs *guiState) openConversation(entry *convEntry) {
	gs.mu.Lock()
	gs.currentConv = entry
	gs.mu.Unlock()

	gs.chatTitle.SetText(entry.title)
	gs.renderConversation(entry.conversationID)
	if entry.isGroup {
		gs.showActiveGroupMembers(entry.groupID)
	} else {
		gs.groupMemberPanel.Hide()
		gs.mu.Lock()
		gs.activeGroupMembers = nil
		gs.mu.Unlock()
		gs.groupMemberList.Refresh()
	}

	gs.setUnread(entry.conversationID, 0)
	go gs.chat.OpenConversation(entry.conversationID)
}

// showActiveGroupMembers 打开群会话时加载成员账号，并在右侧显示userID和角色。
func (gs *guiState) showActiveGroupMembers(groupID uint64) {
	gs.mu.Lock()
	gs.activeGroupMembers = nil
	gs.mu.Unlock()
	gs.groupMemberList.Refresh()
	gs.groupMemberPanel.Show()

	go func() {
		members, err := socialsvc.ListGroupMembers(groupID)
		if err != nil {
			return
		}
		fyne.Do(func() {
			gs.mu.Lock()
			if gs.currentConv == nil || !gs.currentConv.isGroup || gs.currentConv.groupID != groupID {
				gs.mu.Unlock()
				return
			}
			gs.activeGroupMembers = members
			gs.mu.Unlock()
			gs.groupMemberList.Refresh()
		})
	}()
}

// setUnread 更新某个会话的未读角标并刷新列表，供收到新消息/点开会话两处调用。
func (gs *guiState) setUnread(conversationID string, unread uint64) {
	gs.mu.Lock()
	changed := false
	for i := range gs.conversations {
		if gs.conversations[i].conversationID == conversationID {
			gs.conversations[i].unread = unread
			changed = true
			break
		}
	}
	gs.mu.Unlock()
	if changed {
		gs.convList.Refresh()
	}
}

// incrUnread 收到属于非当前会话的消息时，把该会话未读数本地+1，避免每条消息都发
// 一次rpc查询——只有查询会话列表时才做一次权威的服务端派生计算。
func (gs *guiState) incrUnread(conversationID string) {
	gs.mu.Lock()
	changed := false
	for i := range gs.conversations {
		if gs.conversations[i].conversationID == conversationID {
			gs.conversations[i].unread++
			changed = true
			break
		}
	}
	gs.mu.Unlock()
	if changed {
		gs.convList.Refresh()
	}
}
