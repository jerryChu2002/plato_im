package guiclient

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/hardcore-os/plato/common/socialsvc"
)

// showGroupPanel 弹出群管理面板：创建群 + 我的群列表(带查看成员/邀请入口)。
// 对应cui.go的/creategroup /mygroups /invite /members这一组命令。
func (gs *guiState) showGroupPanel() {
	w := gs.app.NewWindow("群组管理")
	w.Resize(fyne.NewSize(480, 420))

	myGroupsTab := gs.buildMyGroupsTab(w)
	tabs := container.NewAppTabs(
		container.NewTabItem("我的群", myGroupsTab),
	)

	createBtn := widget.NewButton("创建群", func() { gs.showCreateGroupDialog(w) })
	w.SetContent(container.NewBorder(createBtn, nil, nil, nil, tabs))
	w.Show()
}

func (gs *guiState) showCreateGroupDialog(parent fyne.Window) {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("群名")

	dialog.ShowForm("创建群", "创建", "取消",
		[]*widget.FormItem{widget.NewFormItem("群名", nameEntry)},
		func(ok bool) {
			if !ok || nameEntry.Text == "" {
				return
			}
			gid, err := socialsvc.CreateGroup(gs.myUserID, nameEntry.Text)
			if err != nil {
				dialog.ShowError(err, parent)
				return
			}
			dialog.ShowInformation("建群成功", fmt.Sprintf("groupID=%d", gid), parent)
			gs.refreshConversations()
		}, parent)
}

func (gs *guiState) buildMyGroupsTab(parent fyne.Window) fyne.CanvasObject {
	var list *widget.List
	list = widget.NewList(
		func() int { return len(gs.cachedMyGroupsForTab) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil, nil,
				container.NewHBox(widget.NewButton("邀请", nil), widget.NewButton("成员", nil)),
				widget.NewLabel(""))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			g := gs.cachedMyGroupsForTab[i]
			row := o.(*fyne.Container)
			label := row.Objects[0].(*widget.Label)
			btns := row.Objects[1].(*fyne.Container)
			inviteBtn := btns.Objects[0].(*widget.Button)
			membersBtn := btns.Objects[1].(*widget.Button)
			label.SetText(fmt.Sprintf("groupID=%d 名称=%s 成员数=%d", g.GetGroupID(), g.GetName(), g.GetMemberCount()))
			groupID := g.GetGroupID()
			inviteBtn.OnTapped = func() { gs.showInviteDialog(parent, groupID, list) }
			membersBtn.OnTapped = func() { gs.showMembersDialog(parent, groupID) }
		},
	)
	gs.reloadMyGroupsTab(list)
	refreshBtn := widget.NewButton("刷新", func() { gs.reloadMyGroupsTab(list) })
	return container.NewBorder(refreshBtn, nil, nil, nil, list)
}

func (gs *guiState) reloadMyGroupsTab(list *widget.List) {
	groups, err := socialsvc.ListMyGroups(gs.myUserID)
	if err == nil {
		gs.cachedMyGroupsForTab = groups
	}
	list.Refresh()
}

func (gs *guiState) showInviteDialog(parent fyne.Window, groupID uint64, refreshList *widget.List) {
	userIDEntry := widget.NewEntry()
	userIDEntry.SetPlaceHolder("要邀请的userID")

	dialog.ShowForm("邀请入群", "邀请", "取消",
		[]*widget.FormItem{widget.NewFormItem("userID", userIDEntry)},
		func(ok bool) {
			if !ok {
				return
			}
			var memberUserID uint64
			if _, err := fmt.Sscanf(userIDEntry.Text, "%d", &memberUserID); err != nil || memberUserID == 0 {
				dialog.ShowError(fmt.Errorf("userID格式不对"), parent)
				return
			}
			if err := socialsvc.InviteToGroup(groupID, gs.myUserID, memberUserID); err != nil {
				dialog.ShowError(err, parent)
				return
			}
			gs.reloadMyGroupsTab(refreshList)
		}, parent)
}

func (gs *guiState) showMembersDialog(parent fyne.Window, groupID uint64) {
	members, err := socialsvc.ListGroupMembers(groupID)
	if err != nil {
		dialog.ShowError(err, parent)
		return
	}
	gs.cachedMembersForTab = members

	list := widget.NewList(
		func() int { return len(gs.cachedMembersForTab) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			m := gs.cachedMembersForTab[i]
			roleText := "普通成员"
			if m.GetRole() == 1 {
				roleText = "群主"
			}
			o.(*widget.Label).SetText(fmt.Sprintf("user=%d %s", m.GetMemberUserID(), roleText))
		},
	)
	d := dialog.NewCustom(fmt.Sprintf("群成员(groupID=%d)", groupID), "关闭", list, parent)
	d.Resize(fyne.NewSize(320, 400))
	d.Show()
}
