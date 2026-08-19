package storage

import (
	"context"
	"testing"
)

func TestGroupMembershipUsesUserIDAndIgnoresLegacyDidRows(t *testing.T) {
	ctx := context.Background()
	sm := NewStorageManager(true)

	legacyGroup := &GroupInfoDAO{Name: "测试群", OwnerDid: 1001}
	if err := sm.db.WithContext(ctx).Create(legacyGroup).Error; err != nil {
		t.Fatalf("create legacy group: %v", err)
	}
	legacyMember := &GroupMemberDAO{GroupID: uint64(legacyGroup.ID), MemberDid: 1001, Role: 1}
	if err := sm.db.WithContext(ctx).Create(legacyMember).Error; err != nil {
		t.Fatalf("create legacy member: %v", err)
	}

	legacyMatched, err := sm.ListMyGroups(ctx, 1001)
	if err != nil {
		t.Fatalf("list groups by userID: %v", err)
	}
	if len(legacyMatched) != 0 {
		t.Fatalf("legacy did row must not become an account group, got %d groups", len(legacyMatched))
	}

	group, err := sm.CreateGroup(ctx, 7, "产品群")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	groupID := uint64(group.ID)
	if err := sm.InviteMembers(ctx, groupID, 7, []uint64{8}); err != nil {
		t.Fatalf("invite member: %v", err)
	}

	groups, err := sm.ListMyGroups(ctx, 8)
	if err != nil {
		t.Fatalf("list member groups: %v", err)
	}
	if len(groups) != 1 || uint64(groups[0].ID) != groupID {
		t.Fatalf("user 8 groups = %#v, want groupID=%d", groups, groupID)
	}

	members, err := sm.ListMembers(ctx, groupID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 2 || members[0].MemberUserID != 7 || members[1].MemberUserID != 8 {
		t.Fatalf("members = %#v, want userIDs [7 8]", members)
	}
}
