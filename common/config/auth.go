package config

import (
	"time"

	"github.com/spf13/viper"
)

const defaultSessionTTL = 7 * 24 * time.Hour

func GetAuthSessionSecret() string {
	return viper.GetString("auth.session_secret")
}

func GetAuthSessionTTL() time.Duration {
	ttl := viper.GetDuration("auth.session_ttl")
	if ttl <= 0 {
		return defaultSessionTTL
	}
	return ttl
}
