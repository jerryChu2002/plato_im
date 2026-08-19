package guiclient

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/hardcore-os/plato/common/convid"
	"github.com/hardcore-os/plato/common/sdk"
)

// recvLoop 常驻消费chat.Recv()，对应cui.go的doRecv goroutine。
// 收到消息后：属于当前打开会话的，按seq重排聊天区；不属于的，只给对应会话的未读角标+1，
// 不打扰用户当前正在看的会话——这是与cui.go终端顺序输出模式的关键差异，GUI下有
// "当前只关注一个会话"的概念，需要显式区分。
func (gs *guiState) recvLoop() {
	for msg := range gs.chat.Recv() {
		if msg == nil {
			continue
		}
		m := msg
		switch m.Type {
		case sdk.MsgTypeText:
			gs.handleIncomingText(m)
		case sdk.MsgTypeSystem:
			fyne.Do(func() { gs.appendSystemLine(m.Content) })
		case sdk.MsgTypeAck:
			// 同cui.go：默认不处理
		}
	}
}

func (gs *guiState) handleIncomingText(m *sdk.Message) {
	gs.mu.Lock()
	current := gs.currentConv
	gs.messages[m.ConversationID] = insertChatLine(gs.messages[m.ConversationID], chatLine{
		name: m.FormUserID, content: m.Content, seq: m.Seq,
	})
	if current != nil && m.ConversationID == current.conversationID {
		gs.mu.Unlock()
		fyne.Do(func() { gs.renderConversation(m.ConversationID) })
		return
	}

	if m.ConversationID == "" {
		gs.mu.Unlock()
		return
	}
	found := false
	for i := range gs.conversations {
		if gs.conversations[i].conversationID == m.ConversationID {
			gs.conversations[i].unread++
			found = true
			break
		}
	}
	if !found {
		if entry, ok := gs.conversationFromMessage(m); ok {
			entry.unread = 1
			gs.conversations = append([]convEntry{entry}, gs.conversations...)
		}
	}
	gs.mu.Unlock()
	fyne.Do(func() { gs.convList.Refresh() })
}

// insertChatLine 按服务端seq插入消息。seq=0的是尚未拿到服务端序号的本地回显，
// 固定放在已确认消息之后；相同seq只保留一条，避免重推越过SDK去重后重复展示。
func insertChatLine(lines []chatLine, line chatLine) []chatLine {
	if line.seq == 0 {
		return append(lines, line)
	}
	index := sort.Search(len(lines), func(i int) bool {
		return lines[i].seq == 0 || lines[i].seq >= line.seq
	})
	if index < len(lines) && lines[index].seq == line.seq {
		return lines
	}
	lines = append(lines, chatLine{})
	copy(lines[index+1:], lines[index:])
	lines[index] = line
	return lines
}

func (gs *guiState) conversationFromMessage(m *sdk.Message) (convEntry, bool) {
	if groupID, ok := convid.ParseGroupID(m.ConversationID); ok {
		return convEntry{
			isGroup:        true,
			groupID:        groupID,
			title:          fmt.Sprintf("[群] %d", groupID),
			conversationID: m.ConversationID,
		}, true
	}
	targetUserID, err := strconv.ParseUint(m.FormUserID, 10, 64)
	if err != nil || targetUserID == 0 || targetUserID == gs.myUserID {
		return convEntry{}, false
	}
	return convEntry{
		did:            targetUserID,
		title:          fmt.Sprintf("user=%d", targetUserID),
		conversationID: m.ConversationID,
	}, true
}

// appendTextLine/appendSystemLine 往聊天区追加一行，text用普通样式，system用告警色高亮
// 区分——对应cui.go里viewPrint对不同msg.Type的展示差异。
func (gs *guiState) appendTextLine(name, content string) {
	seg := &widget.TextSegment{
		Text:  fmt.Sprintf("%s: %s\n", name, content),
		Style: widget.RichTextStyleParagraph,
	}
	gs.msgArea.Segments = append(gs.msgArea.Segments, seg)
	gs.msgArea.Refresh()
	gs.msgScroll.ScrollToBottom()
}

func (gs *guiState) appendSystemLine(content string) {
	seg := &widget.TextSegment{
		Text: fmt.Sprintf("[系统] %s\n", content),
		Style: widget.RichTextStyle{
			ColorName: theme.ColorNameWarning,
			Inline:    false,
		},
	}
	gs.msgArea.Segments = append(gs.msgArea.Segments, seg)
	gs.msgArea.Refresh()
	gs.msgScroll.ScrollToBottom()
}

func (gs *guiState) renderConversation(conversationID string) {
	gs.mu.Lock()
	lines := append([]chatLine(nil), gs.messages[conversationID]...)
	gs.mu.Unlock()
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].seq == 0 {
			return false
		}
		if lines[j].seq == 0 {
			return true
		}
		return lines[i].seq < lines[j].seq
	})
	gs.msgArea.Segments = nil
	for _, line := range lines {
		style := widget.RichTextStyleParagraph
		text := fmt.Sprintf("%s: %s\n", line.name, line.content)
		if line.system {
			style = widget.RichTextStyle{ColorName: theme.ColorNameWarning}
			text = fmt.Sprintf("[系统] %s\n", line.content)
		}
		gs.msgArea.Segments = append(gs.msgArea.Segments, &widget.TextSegment{Text: text, Style: style})
	}
	gs.msgArea.Refresh()
	gs.msgScroll.ScrollToBottom()
}

func (gs *guiState) appendInput(content string) {
	gs.inputBox.SetText(gs.inputBox.Text + content)
	gs.inputBox.CursorRow = len(strings.Split(gs.inputBox.Text, "\n")) - 1
	gs.inputBox.CursorColumn = len([]rune(strings.Split(gs.inputBox.Text, "\n")[gs.inputBox.CursorRow]))
	gs.inputBox.Refresh()
}

// sendCurrentInput 把输入框内容发给当前打开的会话，对应cui.go的doSay。
func (gs *guiState) sendCurrentInput() {
	text := strings.TrimSpace(gs.inputBox.Text)
	if text == "" {
		return
	}
	gs.mu.Lock()
	current := gs.currentConv
	gs.mu.Unlock()
	if current == nil {
		gs.appendSystemLine("还没有选择聊天对象，先在左侧点开一个会话")
		return
	}

	msg := &sdk.Message{
		Type:       sdk.MsgTypeText,
		Name:       fmt.Sprintf("%d", gs.myUserID),
		FormUserID: fmt.Sprintf("%d", gs.myUserID),
		Content:    text,
	}
	var err error
	if current.isGroup {
		err = gs.chat.Send(msg, 0, current.groupID)
	} else {
		err = gs.chat.Send(msg, current.did, 0)
	}
	if err != nil {
		gs.appendSystemLine("发送失败: " + err.Error())
		return
	}
	gs.mu.Lock()
	gs.messages[current.conversationID] = append(gs.messages[current.conversationID], chatLine{
		name: fmt.Sprintf("我(%d)", gs.myUserID), content: text,
	})
	gs.mu.Unlock()
	gs.appendTextLine(fmt.Sprintf("我(%d)", gs.myUserID), text)
	gs.inputBox.SetText("")
}
