package guiclient

import (
	"testing"

	"github.com/hardcore-os/plato/common/sdk"
)

func TestConversationFromMessage(t *testing.T) {
	gs := &guiState{myDid: 10001}

	tests := []struct {
		name   string
		msg    *sdk.Message
		want   convEntry
		wantOK bool
	}{
		{
			name: "direct message",
			msg: &sdk.Message{
				FormUserID:     "10002",
				ConversationID: "10001_10002",
			},
			want: convEntry{
				did:            10002,
				title:          "user=10002",
				conversationID: "10001_10002",
			},
			wantOK: true,
		},
		{
			name: "group message",
			msg: &sdk.Message{
				FormUserID:     "10002",
				ConversationID: "group_42",
			},
			want: convEntry{
				isGroup:        true,
				groupID:        42,
				title:          "[群] 42",
				conversationID: "group_42",
			},
			wantOK: true,
		},
		{
			name: "invalid sender",
			msg: &sdk.Message{
				FormUserID:     "invalid",
				ConversationID: "10001_10002",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := gs.conversationFromMessage(tt.msg)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("entry = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInsertChatLineOrdersBySeq(t *testing.T) {
	lines := []chatLine{{content: "seq 12", seq: 12}}
	lines = insertChatLine(lines, chatLine{content: "seq 11", seq: 11})
	lines = insertChatLine(lines, chatLine{content: "pending", seq: 0})
	lines = insertChatLine(lines, chatLine{content: "seq 13", seq: 13})
	lines = insertChatLine(lines, chatLine{content: "duplicate seq 12", seq: 12})

	if len(lines) != 4 {
		t.Fatalf("len(lines) = %d, want 4", len(lines))
	}
	wantSeqs := []uint64{11, 12, 13, 0}
	for i, wantSeq := range wantSeqs {
		if lines[i].seq != wantSeq {
			t.Fatalf("lines[%d].seq = %d, want %d", i, lines[i].seq, wantSeq)
		}
	}
	if lines[1].content != "seq 12" {
		t.Fatalf("duplicate seq replaced original content: %q", lines[1].content)
	}
}
