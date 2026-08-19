package auth

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestSessionTokenBindsUserAndDevice(t *testing.T) {
	viper.Set("auth.session_secret", "test-secret")
	viper.Set("auth.session_ttl", time.Hour)
	now := time.Unix(1000, 0)
	token := IssueSessionToken(10, 20, now)

	if err := VerifySessionToken(token, 10, 20, now.Add(time.Minute)); err != nil {
		t.Fatalf("VerifySessionToken() error = %v", err)
	}
	if err := VerifySessionToken(token, 11, 20, now.Add(time.Minute)); err == nil {
		t.Fatal("VerifySessionToken() accepted another user")
	}
	if err := VerifySessionToken(token, 10, 21, now.Add(time.Minute)); err == nil {
		t.Fatal("VerifySessionToken() accepted another device")
	}
	if err := VerifySessionToken(token, 10, 20, now.Add(2*time.Hour)); err == nil {
		t.Fatal("VerifySessionToken() accepted expired token")
	}
}
