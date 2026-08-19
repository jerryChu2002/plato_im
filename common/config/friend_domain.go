package config

import "github.com/spf13/viper"

func GetDomainFriendServerName() string {
	return viper.GetString("friend_domain.service_name")
}

func GetDomainFriendServerAddr() string {
	return viper.GetString("friend_domain.service_addr")
}

func GetDomainFriendServerPoint() int {
	return viper.GetInt("friend_domain.service_port")
}

func GetDomainFriendRPCWeight() int {
	return viper.GetInt("friend_domain.weight")
}

func GetDomainFriendDBDNS() string {
	return viper.GetString("friend_domain.db_dns")
}
