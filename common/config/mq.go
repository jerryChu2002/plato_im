package config

import "github.com/spf13/viper"

func GetKafkaBrokers() []string {
	return viper.GetStringSlice("kafka.brokers")
}

func GetKafkaChatTopic() string {
	return viper.GetString("kafka.chat_topic")
}

func GetKafkaConsumerGroup() string {
	return viper.GetString("kafka.consumer_group")
}
