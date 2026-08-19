package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hardcore-os/plato/common/config"
)

var errInvalidSession = errors.New("invalid session token")

// IssueSessionToken 签发一个同时绑定账号和设备的短会话凭证。
func IssueSessionToken(userID, deviceID uint64, now time.Time) string {
	expiresAt := now.Add(config.GetAuthSessionTTL()).Unix()
	payload := fmt.Sprintf("%d:%d:%d", userID, deviceID, expiresAt)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sign(payload)
}

// VerifySessionToken 校验签名、有效期以及令牌是否属于给定账号和设备。
func VerifySessionToken(token string, userID, deviceID uint64, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return errInvalidSession
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errInvalidSession
	}
	payload := string(payloadBytes)
	if !hmac.Equal([]byte(parts[1]), []byte(sign(payload))) {
		return errInvalidSession
	}
	fields := strings.Split(payload, ":")
	if len(fields) != 3 {
		return errInvalidSession
	}
	tokenUserID, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || tokenUserID != userID {
		return errInvalidSession
	}
	tokenDeviceID, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil || tokenDeviceID != deviceID {
		return errInvalidSession
	}
	expiresAt, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || now.Unix() >= expiresAt {
		return errInvalidSession
	}
	return nil
}

func sign(payload string) string {
	mac := hmac.New(sha256.New, []byte(config.GetAuthSessionSecret()))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
