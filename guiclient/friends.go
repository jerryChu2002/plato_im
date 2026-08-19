package guiclient

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/hardcore-os/plato/common/socialsvc"
)

// showFriendPanel 弹出好友管理面板：添加好友 + 好友列表(带删除) + 收到的申请(带同意/拒绝)
// + 我发出的申请(带状态)。对应cui.go的/addfriend /friends /delfriend /requests
// /accept /reject /sentrequests 这一组命令。
func (gs *guiState) showFriendPanel() {
	w := gs.app.NewWindow("好友管理")
	w.Resize(fyne.NewSize(480, 420))

	friendsTab := gs.buildFriendListTab(w)
	pendingTab := gs.buildPendingRequestsTab(w)
	sentTab := gs.buildSentRequestsTab(w)

	tabs := container.NewAppTabs(
		container.NewTabItem("好友列表", friendsTab),
		container.NewTabItem("收到的申请", pendingTab),
		container.NewTabItem("我发出的申请", sentTab),
	)

	addBtn := widget.NewButton("添加好友", func() { gs.showAddFriendDialog(w) })
	w.SetContent(container.NewBorder(addBtn, nil, nil, nil, tabs))
	w.Show()
}

func (gs *guiState) showAddFriendDialog(parent fyne.Window) {
	didEntry := widget.NewEntry()
	didEntry.SetPlaceHolder("目标did")
	msgEntry := widget.NewEntry()
	msgEntry.SetPlaceHolder("附言(可选)")

	dialog.ShowForm("添加好友", "发送申请", "取消",
		[]*widget.FormItem{
			widget.NewFormItem("did", didEntry),
			widget.NewFormItem("附言", msgEntry),
		},
		func(ok bool) {
			if !ok {
				return
			}
			var targetDid uint64
			if _, err := fmt.Sscanf(didEntry.Text, "%d", &targetDid); err != nil || targetDid == 0 {
				dialog.ShowError(fmt.Errorf("did格式不对"), parent)
				return
			}
			reqID, err := socialsvc.AddFriend(gs.myDid, targetDid, msgEntry.Text)
			if err != nil {
				dialog.ShowError(err, parent)
				return
			}
			dialog.ShowInformation("已发送", fmt.Sprintf("好友申请已发送，requestID=%d", reqID), parent)
		}, parent)
}

func (gs *guiState) buildFriendListTab(parent fyne.Window) fyne.CanvasObject {
	var list *widget.List
	list = widget.NewList(
		func() int { return len(gs.cachedFriendsForTab) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil, nil, widget.NewButton("删除", nil), widget.NewLabel(""))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			f := gs.cachedFriendsForTab[i]
			row := o.(*fyne.Container)
			label := row.Objects[0].(*widget.Label)
			btn := row.Objects[1].(*widget.Button)
			title := fmt.Sprintf("did=%d", f.GetDid())
			if f.GetRemark() != "" {
				title = fmt.Sprintf("%s 备注=%s", title, f.GetRemark())
			}
			label.SetText(title)
			did := f.GetDid()
			btn.OnTapped = func() {
				if err := socialsvc.DeleteFriend(gs.myDid, did); err != nil {
					dialog.ShowError(err, parent)
					return
				}
				gs.reloadFriendListTab(list)
				gs.refreshConversations()
			}
		},
	)
	gs.reloadFriendListTab(list)
	refreshBtn := widget.NewButton("刷新", func() { gs.reloadFriendListTab(list) })
	return container.NewBorder(refreshBtn, nil, nil, nil, list)
}

func (gs *guiState) reloadFriendListTab(list *widget.List) {
	friends, err := socialsvc.ListFriends(gs.myDid)
	if err == nil {
		gs.cachedFriendsForTab = friends
	}
	list.Refresh()
}

func (gs *guiState) buildPendingRequestsTab(parent fyne.Window) fyne.CanvasObject {
	var list *widget.List
	list = widget.NewList(
		func() int { return len(gs.cachedPendingForTab) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil, nil,
				container.NewHBox(widget.NewButton("同意", nil), widget.NewButton("拒绝", nil)),
				widget.NewLabel(""))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			r := gs.cachedPendingForTab[i]
			row := o.(*fyne.Container)
			label := row.Objects[0].(*widget.Label)
			btns := row.Objects[1].(*fyne.Container)
			agreeBtn := btns.Objects[0].(*widget.Button)
			rejectBtn := btns.Objects[1].(*widget.Button)
			label.SetText(fmt.Sprintf("requestID=%d from did=%d 附言:%s", r.GetID(), r.GetFromDid(), r.GetMessage()))
			reqID := r.GetID()
			handle := func(agree bool) {
				if err := socialsvc.HandleFriendRequest(reqID, gs.myDid, agree); err != nil {
					dialog.ShowError(err, parent)
					return
				}
				gs.reloadPendingRequestsTab(list)
				gs.refreshConversations()
			}
			agreeBtn.OnTapped = func() { handle(true) }
			rejectBtn.OnTapped = func() { handle(false) }
		},
	)
	gs.reloadPendingRequestsTab(list)
	refreshBtn := widget.NewButton("刷新", func() { gs.reloadPendingRequestsTab(list) })
	return container.NewBorder(refreshBtn, nil, nil, nil, list)
}

func (gs *guiState) reloadPendingRequestsTab(list *widget.List) {
	reqs, err := socialsvc.ListPendingRequests(gs.myDid)
	if err == nil {
		gs.cachedPendingForTab = reqs
	}
	list.Refresh()
}

func (gs *guiState) buildSentRequestsTab(parent fyne.Window) fyne.CanvasObject {
	list := widget.NewList(
		func() int { return len(gs.cachedSentForTab) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			r := gs.cachedSentForTab[i]
			o.(*widget.Label).SetText(fmt.Sprintf("requestID=%d to did=%d 状态:%s", r.GetID(), r.GetToDid(), r.GetStatus().String()))
		},
	)
	gs.reloadSentRequestsTab(list)
	refreshBtn := widget.NewButton("刷新", func() { gs.reloadSentRequestsTab(list) })
	return container.NewBorder(refreshBtn, nil, nil, nil, list)
}

func (gs *guiState) reloadSentRequestsTab(list *widget.List) {
	reqs, err := socialsvc.ListSentRequests(gs.myDid)
	if err == nil {
		gs.cachedSentForTab = reqs
	}
	list.Refresh()
}
