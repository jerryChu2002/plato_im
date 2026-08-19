package config

import "github.com/spf13/viper"

func GetDomainMessageServerName() string {
	return viper.GetString("message_domain.service_name")
}

func GetDomainMessageServerAddr() string {
	return viper.GetString("message_domain.service_addr")
}

func GetDomainMessageServerPoint() int {
	return viper.GetInt("message_domain.service_port")
}

func GetDomainMessageRPCWeight() int {
	return viper.GetInt("message_domain.weight")
}

func GetDomainMessageDBDNS() string {
	return viper.GetString("message_domain.db_dns")
}
