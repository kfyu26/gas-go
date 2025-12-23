package main

import (
	"strconv"

	"github.com/shopspring/decimal"
)

const (
	defaultDBPath           = "data/gas_usage.db"
	defaultMQTTHost         = "mqtturl"
	defaultMQTTPort         = 8883
	defaultMQTTUser         = "user"
	defaultMQTTPassword     = "passwrod"
	defaultMQTTTopic        = "homeassistant/sensor/ir_counter/state"
	defaultMQTTTLS          = true
	defaultMQTTTLSInsecure  = false
	defaultGasPerPulse      = "0.001"
	defaultInitialGas       = "100.000"
	defaultMeterBase        = "0.000"
	defaultTGThreshold      = "5.0"
	defaultTGNotifyTimes    = 2
	defaultTGNotifyInterval = "2.0"
)

func loadSettings(store *Store) (Settings, error) {
	settings := Settings{}

	var err error
	settings.GasPerPulse, err = store.GetSetting("gas_per_pulse", defaultGasPerPulse)
	if err != nil {
		return settings, err
	}
	settings.InitialGas, err = store.GetSetting("initial_gas", defaultInitialGas)
	if err != nil {
		return settings, err
	}
	basePulses, err := store.GetSetting("initial_gas_base_pulses", "0")
	if err != nil {
		return settings, err
	}
	settings.InitialBasePulses, _ = strconv.ParseInt(basePulses, 10, 64)

	settings.MeterBaseM3, err = store.GetSetting("meter_base_m3", defaultMeterBase)
	if err != nil {
		return settings, err
	}
	settings.DesiredMeterM3, err = store.GetSetting("desired_meter_m3", defaultMeterBase)
	if err != nil {
		return settings, err
	}

	settings.MQTTHost, err = store.GetSetting("mqtt_host", defaultMQTTHost)
	if err != nil {
		return settings, err
	}
	port, err := store.GetSetting("mqtt_port", strconv.Itoa(defaultMQTTPort))
	if err != nil {
		return settings, err
	}
	settings.MQTTPort, _ = strconv.Atoi(port)
	settings.MQTTUser, err = store.GetSetting("mqtt_user", defaultMQTTUser)
	if err != nil {
		return settings, err
	}
	settings.MQTTPassword, err = store.GetSetting("mqtt_pass", defaultMQTTPassword)
	if err != nil {
		return settings, err
	}
	settings.MQTTTopic, err = store.GetSetting("mqtt_topic", defaultMQTTTopic)
	if err != nil {
		return settings, err
	}
	mqttTLS, err := store.GetSetting("mqtt_tls", "1")
	if err != nil {
		return settings, err
	}
	settings.MQTTTLS = mqttTLS == "1"
	mqttTLSInsecure, err := store.GetSetting("mqtt_tls_insecure", "0")
	if err != nil {
		return settings, err
	}
	settings.MQTTTLSInsecure = mqttTLSInsecure == "1"

	tgEnabled, err := store.GetSetting("tg_notify_enabled", "0")
	if err != nil {
		return settings, err
	}
	settings.TGEnabled = tgEnabled == "1"
	settings.TGBotToken, err = store.GetSetting("tg_bot_token", "")
	if err != nil {
		return settings, err
	}
	settings.TGChatID, err = store.GetSetting("tg_chat_id", "")
	if err != nil {
		return settings, err
	}
	settings.TGAPIEndpoint, err = store.GetSetting("tg_api_endpoint", "")
	if err != nil {
		return settings, err
	}
	settings.TGThreshold, err = store.GetSetting("tg_threshold", defaultTGThreshold)
	if err != nil {
		return settings, err
	}
	notifyTimes, err := store.GetSetting("tg_notify_times", strconv.Itoa(defaultTGNotifyTimes))
	if err != nil {
		return settings, err
	}
	settings.TGNotifyTimes, _ = strconv.Atoi(notifyTimes)
	settings.TGNotifyIntervalHour, err = store.GetSetting("tg_notify_interval_hours", defaultTGNotifyInterval)
	if err != nil {
		return settings, err
	}

	return settings, nil
}

func parseDecimal(value string, fallback string) decimal.Decimal {
	dec, err := decimal.NewFromString(value)
	if err != nil {
		dec, _ = decimal.NewFromString(fallback)
	}
	return dec
}

func quantize3(value decimal.Decimal) decimal.Decimal {
	return value.Round(3)
}