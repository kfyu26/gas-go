package main

type Event struct {
	Timestamp int64 `json:"timestamp"`
	Count     int64 `json:"count"`
}

type Settings struct {
	GasPerPulse          string `json:"gas_per_pulse"`
	InitialGas           string `json:"initial_gas"`
	InitialBasePulses    int64  `json:"initial_base_pulses"`
	MeterBaseM3          string `json:"meter_base_m3"`
	DesiredMeterM3       string `json:"desired_meter_m3"`
	MQTTHost             string `json:"mqtt_host"`
	MQTTPort             int    `json:"mqtt_port"`
	MQTTUser             string `json:"mqtt_user"`
	MQTTPassword         string `json:"mqtt_password"`
	MQTTTopic            string `json:"mqtt_topic"`
	MQTTTLS              bool   `json:"mqtt_tls"`
	MQTTTLSInsecure      bool   `json:"mqtt_tls_insecure"`
	TGEnabled            bool   `json:"tg_enabled"`
	TGBotToken           string `json:"tg_bot_token"`
	TGChatID             string `json:"tg_chat_id"`
	TGAPIEndpoint        string `json:"tg_api_endpoint"`
	TGThreshold          string `json:"tg_threshold"`
	TGNotifyTimes        int    `json:"tg_notify_times"`
	TGNotifyIntervalHour string `json:"tg_notify_interval_hours"`
}

type Metrics struct {
	TodayGas     string `json:"today_gas"`
	WeekGas      string `json:"week_gas"`
	MonthGas     string `json:"month_gas"`
	TotalUsedGas string `json:"total_used_gas"`
	MeterReading string `json:"meter_reading"`
	RemainGas    string `json:"remain_gas"`
	MQTTStatus   string `json:"mqtt_status"`
	LastMsgTime  string `json:"last_msg_time"`
}