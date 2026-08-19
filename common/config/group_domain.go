package config

import "github.com/spf13/viper"

func GetDomainGroupServerName() string {
	return viper.GetString("group_domain.service_name")
}

func GetDomainGroupServerAddr() string {
	return viper.GetString("group_domain.service_addr")
}

func GetDomainGroupServerPoint() int {
	return viper.GetInt("group_domain.service_port")
}

func GetDomainGroupRPCWeight() int {
	return viper.GetInt("group_domain.weight")
}

func GetDomainGroupDBDNS() string {
	return viper.GetString("group_domain.db_dns")
}
