package storage

import (
	"context"
	"errors"
	"testing"
)

func TestRegisterLoginAndBindDevices(t *testing.T) {
	sm := NewStorageManager(true)
	ctx := context.Background()

	userID, err := sm.Register(ctx, "Alice", "secret1", "Alice", 101)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if userID == 0 {
		t.Fatal("Register() returned zero userID")
	}
	if _, err := sm.Login(ctx, "alice", "wrong-password", 102); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() wrong password error = %v", err)
	}
	loggedInUserID, err := sm.Login(ctx, "ALICE", "secret1", 102)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if loggedInUserID != userID {
		t.Fatalf("Login() userID = %d, want %d", loggedInUserID, userID)
	}
	deviceIDs, err := sm.ListUserDeviceIDs(ctx, userID)
	if err != nil {
		t.Fatalf("ListUserDeviceIDs() error = %v", err)
	}
	if len(deviceIDs) != 2 || deviceIDs[0] != 101 || deviceIDs[1] != 102 {
		t.Fatalf("ListUserDeviceIDs() = %v, want [101 102]", deviceIDs)
	}
}

func TestDeviceCannotBelongToTwoUsers(t *testing.T) {
	sm := NewStorageManager(true)
	ctx := context.Background()

	if _, err := sm.Register(ctx, "alice", "secret1", "Alice", 101); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.Register(ctx, "bob", "secret2", "Bob", 101); !errors.Is(err, ErrDeviceBelongsToUser) {
		t.Fatalf("Register() reused device error = %v", err)
	}
}
