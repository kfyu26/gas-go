package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type MQTTWorker struct {
	store  *Store
	config func() (Settings, error)
	client mqtt.Client
	stopCh chan struct{}
	status string
}

func NewMQTTWorker(store *Store, config func() (Settings, error)) *MQTTWorker {
	return &MQTTWorker{
		store:  store,
		config: config,
		stopCh: make(chan struct{}),
		status: "not_started",
	}
}

func (w *MQTTWorker) Status() string {
	return w.status
}

func (w *MQTTWorker) Start() {
	go func() {
		for {
			select {
			case <-w.stopCh:
				return
			default:
			}

			settings, err := w.config()
			if err != nil {
				w.status = fmt.Sprintf("config_error: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}

			clientID := fmt.Sprintf("gas-monitor-%d", time.Now().UnixNano())
			opts := mqtt.NewClientOptions().
				AddBroker(fmt.Sprintf("%s://%s:%d", brokerScheme(settings.MQTTTLS), settings.MQTTHost, settings.MQTTPort)).
				SetClientID(clientID).
				SetAutoReconnect(true).
				SetConnectRetry(true).
				SetConnectRetryInterval(3 * time.Second)

			if settings.MQTTUser != "" {
				opts.SetUsername(settings.MQTTUser)
				opts.SetPassword(settings.MQTTPassword)
			}
			if settings.MQTTTLS {
				opts.SetTLSConfig(defaultTLSConfig(settings.MQTTTLSInsecure))
			}

			opts.OnConnect = func(c mqtt.Client) {
				w.status = "connected"
				if token := c.Subscribe(settings.MQTTTopic, 0, w.handleMessage); token.Wait() && token.Error() != nil {
					_ = w.store.SetSetting("last_mqtt_error", token.Error().Error())
				}
				_ = w.store.SetSetting("mqtt_topic_subscribed", settings.MQTTTopic)
			}
			opts.OnConnectionLost = func(_ mqtt.Client, err error) {
				w.status = fmt.Sprintf("connection_lost: %v", err)
				_ = w.store.SetSetting("last_mqtt_error", err.Error())
			}

			w.status = "connecting"
			client := mqtt.NewClient(opts)
			w.client = client
			if token := client.Connect(); token.Wait() && token.Error() != nil {
				w.status = fmt.Sprintf("connect_failed: %v", token.Error())
				_ = w.store.SetSetting("mqtt_status", w.status)
				time.Sleep(5 * time.Second)
				continue
			}
			_ = w.store.SetSetting("mqtt_status", "connected")

			for {
				select {
				case <-w.stopCh:
					client.Disconnect(250)
					return
				default:
					_ = w.store.SetSetting("mqtt_status", w.status)
					time.Sleep(5 * time.Second)
				}
			}
		}
	}()
}

func (w *MQTTWorker) Stop() {
	close(w.stopCh)
	if w.client != nil && w.client.IsConnected() {
		w.client.Disconnect(250)
	}
}

type mqttPayload struct {
	Count     int64 `json:"count"`
	Timestamp int64 `json:"timestamp"`
}

func (w *MQTTWorker) handleMessage(_ mqtt.Client, msg mqtt.Message) {
	var payload mqttPayload
	if err := json.Unmarshal(msg.Payload(), &payload); err != nil {
		_ = w.store.SetSetting("last_mqtt_error", err.Error())
		log.Printf("MQTT payload parse error: %v, payload: %s", err, string(msg.Payload()))
		return
	}
	if payload.Timestamp == 0 {
		payload.Timestamp = time.Now().Unix()
	}
	
	// 添加调试日志
	log.Printf("MQTT received: count=%d, timestamp=%d", payload.Count, payload.Timestamp)
	
	if err := w.store.InsertEvent(payload.Timestamp, payload.Count); err != nil {
		_ = w.store.SetSetting("last_mqtt_error", err.Error())
		log.Printf("DB insert error: %v", err)
		return
	}
	
	_ = w.store.SetSetting("last_msg_ts", fmt.Sprintf("%d", payload.Timestamp))
	_ = w.store.SetSetting("last_msg_count", fmt.Sprintf("%d", payload.Count))
	log.Printf("MQTT data saved to database")
}

func brokerScheme(useTLS bool) string {
	if useTLS {
		return "ssl"
	}
	return "tcp"
}