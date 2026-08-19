//go:build integration

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hardcore-os/plato/common/cache"
	"github.com/hardcore-os/plato/common/config"
	messagedto "github.com/hardcore-os/plato/common/idl/domain/message"
	"github.com/hardcore-os/plato/domain/message/storage"
)

func TestPullOfflineMsgsUsesDatabasePagesThenRecentCache(t *testing.T) {
	config.Init("../../../../plato.docker.yaml")
	ctx := context.Background()
	cache.InitRedis(ctx)

	conversationID := fmt.Sprintf("integration_recent_%d", time.Now().UnixNano())
	cacheKeys := []string{
		fmt.Sprintf(cache.RecentMsgKey, conversationID),
		fmt.Sprintf(cache.RecentMsgMetaKey, conversationID),
	}
	t.Cleanup(func() { _ = cache.Del(ctx, cacheKeys...) })

	sm := storage.NewStorageManager(true)
	for seq := uint64(1); seq <= 205; seq++ {
		isNew, err := sm.SaveMsg(ctx, &storage.MessageDAO{
			ConversationID: conversationID,
			FromUserID:     1,
			ToUserID:       2,
			Content:        []byte(fmt.Sprintf("message-%d", seq)),
			Seq:            seq,
			ClientMsgID:    fmt.Sprintf("client-%d", seq),
			CreateTimeMS:   int64(seq),
		})
		if err != nil || !isNew {
			t.Fatalf("seed seq=%d: isNew=%v err=%v", seq, isNew, err)
		}
	}

	svc := NewService(sm)
	page, err := svc.PullOfflineMsgs(ctx, &PullOfflineMsgsRequest{
		ConversationID: conversationID,
		FromSeq:        0,
		Limit:          30,
	})
	if err != nil || page.GetBase().GetCode() != 0 {
		t.Fatalf("database page: response=%v err=%v", page, err)
	}
	assertSeqRange(t, page.GetMsgs(), 1, 30)
	if !page.GetHasMore() {
		t.Fatal("database page hasMore=false, want true")
	}

	// 205条消息回填后，Redis完整覆盖106~205；请求limit=10仍应一次返回全部100条。
	recent, err := svc.PullOfflineMsgs(ctx, &PullOfflineMsgsRequest{
		ConversationID: conversationID,
		FromSeq:        105,
		Limit:          10,
	})
	if err != nil || recent.GetBase().GetCode() != 0 {
		t.Fatalf("recent cache: response=%v err=%v", recent, err)
	}
	assertSeqRange(t, recent.GetMsgs(), 106, 205)
	if recent.GetHasMore() {
		t.Fatal("recent cache hasMore=true, want false")
	}

	// fromSeq早于缓存边界时必须按数据库limit分页，不能返回不完整的ZSET窗口。
	older, err := svc.PullOfflineMsgs(ctx, &PullOfflineMsgsRequest{
		ConversationID: conversationID,
		FromSeq:        104,
		Limit:          10,
	})
	if err != nil || older.GetBase().GetCode() != 0 {
		t.Fatalf("older database page: response=%v err=%v", older, err)
	}
	assertSeqRange(t, older.GetMsgs(), 105, 114)
	if !older.GetHasMore() {
		t.Fatal("older database page hasMore=false, want true")
	}
}

func TestRecentMessageCacheIsIdempotentAndRepairsMissingDataKey(t *testing.T) {
	config.Init("../../../../plato.docker.yaml")
	ctx := context.Background()
	cache.InitRedis(ctx)

	conversationID := fmt.Sprintf("integration_repair_%d", time.Now().UnixNano())
	dataKey := fmt.Sprintf(cache.RecentMsgKey, conversationID)
	metaKey := fmt.Sprintf(cache.RecentMsgMetaKey, conversationID)
	t.Cleanup(func() { _ = cache.Del(ctx, dataKey, metaKey) })

	sm := storage.NewStorageManager(true)
	for seq := uint64(1); seq <= 3; seq++ {
		_, err := sm.SaveMsg(ctx, &storage.MessageDAO{
			ConversationID: conversationID,
			FromUserID:     1,
			ToUserID:       2,
			Content:        []byte(fmt.Sprintf("message-%d", seq)),
			Seq:            seq,
			ClientMsgID:    fmt.Sprintf("client-%d", seq),
			CreateTimeMS:   int64(seq),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(sm)
	if _, err := svc.PullOfflineMsgs(ctx, &PullOfflineMsgsRequest{ConversationID: conversationID, Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if err := cache.Del(ctx, dataKey); err != nil {
		t.Fatal(err)
	}
	repaired, err := svc.PullOfflineMsgs(ctx, &PullOfflineMsgsRequest{ConversationID: conversationID, FromSeq: 1, Limit: 1})
	if err != nil || repaired.GetBase().GetCode() != 0 {
		t.Fatalf("repair pull: response=%v err=%v", repaired, err)
	}
	assertSeqRange(t, repaired.GetMsgs(), 2, 2)
	if !repaired.GetHasMore() {
		t.Fatal("repair database page hasMore=false, want true")
	}

	payload := []byte("same-message")
	if err := cache.AddRecentMessage(ctx, conversationID, cache.ScoredMessage{Seq: 3, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if err := cache.AddRecentMessage(ctx, conversationID, cache.ScoredMessage{Seq: 3, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	payloads, hit, _, err := cache.GetRecentMessagesAfter(ctx, conversationID, 2)
	if err != nil || !hit || len(payloads) != 1 {
		t.Fatalf("idempotent cache: count=%d hit=%v err=%v", len(payloads), hit, err)
	}
}

func assertSeqRange(t *testing.T, msgs []*messagedto.ChatMsgDTO, first, last uint64) {
	t.Helper()
	wantLen := int(last - first + 1)
	if len(msgs) != wantLen {
		t.Fatalf("message count=%d, want %d", len(msgs), wantLen)
	}
	for i, msg := range msgs {
		wantSeq := first + uint64(i)
		if msg.GetSeq() != wantSeq {
			t.Fatalf("message[%d].seq=%d, want %d", i, msg.GetSeq(), wantSeq)
		}
	}
}
