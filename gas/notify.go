package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

type telegramPayload struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

func sendTelegramNotification(botToken, chatID, message, apiEndpoint string) error {
	endpoint := "https://api.telegram.org"
	if apiEndpoint != "" {
		endpoint = apiEndpoint
	}
	url := fmt.Sprintf("%s/bot%s/sendMessage", endpoint, botToken)

	payload := telegramPayload{ChatID: chatID, Text: message, ParseMode: "HTML"}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram status: %s", resp.Status)
	}
	return nil
}

func checkAndNotifyLowGas(store *Store, settings Settings, remain decimal.Decimal) {
	if !settings.TGEnabled {
		return
	}
	if settings.TGBotToken == "" || settings.TGChatID == "" {
		return
	}

	threshold := parseDecimal(settings.TGThreshold, defaultTGThreshold)
	if remain.GreaterThanOrEqual(threshold) {
		_ = store.SetSetting("low_gas_notify_count", "0")
		_ = store.SetSetting("low_gas_first_notify_time", "0")
		_ = store.SetSetting("last_notify_time", "0")
		return
	}

	lastNotifyRaw, _ := store.GetSetting("last_notify_time", "0")
	lastNotify, _ := strconv.ParseInt(lastNotifyRaw, 10, 64)
	now := time.Now().Unix()
	if lastNotify > 0 && now-lastNotify < 30 {
		return
	}

	notifyCountRaw, _ := store.GetSetting("low_gas_notify_count", "0")
	notifyCount, _ := strconv.ParseInt(notifyCountRaw, 10, 64)
	firstNotifyRaw, _ := store.GetSetting("low_gas_first_notify_time", "0")
	firstNotify, _ := strconv.ParseInt(firstNotifyRaw, 10, 64)

	intervalHours, err := decimal.NewFromString(settings.TGNotifyIntervalHour)
	if err != nil {
		intervalHours = decimal.RequireFromString(defaultTGNotifyInterval)
	}
	intervalSeconds := intervalHours.Mul(decimal.NewFromInt(3600)).IntPart()

	shouldNotify := false
	notifyType := ""
	if notifyCount == 0 {
		shouldNotify = true
		notifyType = "首次预警"
	} else if notifyCount < int64(settings.TGNotifyTimes) {
		elapsed := now - firstNotify
		required := intervalSeconds * notifyCount
		if elapsed >= required {
			shouldNotify = true
			if notifyCount == 1 {
				notifyType = "二次提醒"
			} else {
				notifyType = fmt.Sprintf("第%d次提醒", notifyCount+1)
			}
		}
	}

	if !shouldNotify {
		return
	}

	message := fmt.Sprintf("🔥 <b>燃气余量预警</b> [%s]\n\n⚠️ 当前剩余燃气：<b>%s m³</b>\n📉 已低于阈值：<b>%s m³</b>\n\n💡 请及时充值燃气额度！\n\n⏰ 通知时间：%s", notifyType, remain.StringFixed(3), threshold.StringFixed(3), time.Now().Format("2006-01-02 15:04:05"))

	_ = store.SetSetting("last_notify_time", fmt.Sprintf("%d", now))
	if err := sendTelegramNotification(settings.TGBotToken, settings.TGChatID, message, settings.TGAPIEndpoint); err != nil {
		_ = store.SetSetting("last_notify_time", lastNotifyRaw)
		return
	}
	if notifyCount == 0 {
		_ = store.SetSetting("low_gas_first_notify_time", fmt.Sprintf("%d", now))
	}
	_ = store.SetSetting("low_gas_notify_count", fmt.Sprintf("%d", notifyCount+1))
}