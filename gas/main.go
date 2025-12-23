package main

import (
	"html/template"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var localTZ = time.FixedZone("CST", 8*3600)

func main() {
	dbPath := getenv("GAS_DB_PATH", defaultDBPath)
	store, err := NewStore(dbPath)
	if err != nil {
		log.Fatalf("init db: %v", err)
	}
	defer store.Close()

	worker := NewMQTTWorker(store, func() (Settings, error) {
		return loadSettings(store)
	})
	worker.Start()

	// 解析模板
	tmpl, err := template.ParseFiles("templates/index.html")
	if err != nil {
		log.Fatalf("parse template: %v", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// 静态文件服务
	r.Get("/static/*", func(w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))).ServeHTTP(w, r)
	})

	// 主页路由 - 使用模板渲染
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		if err := tmpl.Execute(w, nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	// 数据设置工具页面
	r.Get("/data-import", func(w http.ResponseWriter, r *http.Request) {
		dataTmpl, err := template.ParseFiles("templates/data-import.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := dataTmpl.Execute(w, nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	r.Route("/api", func(r chi.Router) {
		r.Get("/settings", func(w http.ResponseWriter, r *http.Request) {
			settings, err := loadSettings(store)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			respondJSON(w, settings)
		})

		r.Put("/settings", func(w http.ResponseWriter, r *http.Request) {
			var payload Settings
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				respondError(w, http.StatusBadRequest, err)
				return
			}
			if err := saveSettings(store, payload); err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			respondJSON(w, map[string]string{"status": "ok"})
		})

		r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
			metrics, err := computeMetrics(store)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			respondJSON(w, metrics)
		})

		r.Get("/hourly", func(w http.ResponseWriter, r *http.Request) {
			now := time.Now().In(localTZ)
			hourly, err := calcHourlyPulsesToday(store, now)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			respondJSON(w, hourly)
		})

		r.Get("/monthly", func(w http.ResponseWriter, r *http.Request) {
			now := time.Now().In(localTZ)
			monthly, err := calcMonthlyPulsesCurrentYear(store, now)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			respondJSON(w, monthly)
		})

		r.Get("/recent", func(w http.ResponseWriter, r *http.Request) {
			limit := 100
			if raw := r.URL.Query().Get("limit"); raw != "" {
				if v, err := strconv.Atoi(raw); err == nil {
					limit = v
				}
			}
			recent, err := store.FetchRecentEvents(limit)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			respondJSON(w, recent)
		})

		r.Get("/debug/events", func(w http.ResponseWriter, r *http.Request) {
			events, err := store.FetchAllEvents()
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			respondJSON(w, map[string]interface{}{
				"total_events": len(events),
				"latest_event": func() interface{} {
					if len(events) > 0 {
						return events[len(events)-1]
					}
					return nil
				}(),
				"recent_events": func() []Event {
					if len(events) > 10 {
						return events[len(events)-10:]
					}
					return events
				}(),
			})
		})

		r.Get("/debug/metrics", func(w http.ResponseWriter, r *http.Request) {
			settings, err := loadSettings(store)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			
			now := time.Now().In(localTZ)
			todayStart := startOfDay(now)
			
			todayPulses, _ := calcUsagePulsesByDelta(store, todayStart, now)
			totalPulses, _ := calcTotalPulsesByDelta(store)
			hourly, _ := calcHourlyPulsesToday(store, now)
			
			respondJSON(w, map[string]interface{}{
				"settings": settings,
				"now": now.Format("2006-01-02 15:04:05"),
				"today_start": todayStart.Format("2006-01-02 15:04:05"),
				"today_pulses": todayPulses,
				"total_pulses": totalPulses,
				"hourly_data": hourly,
			})
		})

		// 数据插入端点
		r.Post("/debug/insert-event", func(w http.ResponseWriter, r *http.Request) {
			var payload struct {
				Timestamp int64 `json:"timestamp"`
				Count     int64 `json:"count"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				respondError(w, http.StatusBadRequest, err)
				return
			}
			if err := store.InsertEvent(payload.Timestamp, payload.Count); err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			respondJSON(w, map[string]string{"status": "success", "message": "数据插入成功"})
		})

		// 数据删除端点
		r.Post("/debug/delete-event", func(w http.ResponseWriter, r *http.Request) {
			var payload struct {
				Timestamp int64 `json:"timestamp"`
				Count     int64 `json:"count"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				respondError(w, http.StatusBadRequest, err)
				return
			}
			_, err := store.db.Exec(`DELETE FROM events WHERE ts = ? AND count = ?`, payload.Timestamp, payload.Count)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			respondJSON(w, map[string]string{"status": "success", "message": "数据删除成功"})
		})

		// 批量插入端点
		r.Post("/debug/batch-insert-events", func(w http.ResponseWriter, r *http.Request) {
			var payload struct {
				Events []struct {
					Timestamp int64 `json:"timestamp"`
					Count     int64 `json:"count"`
				} `json:"events"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				respondError(w, http.StatusBadRequest, err)
				return
			}
			
			tx, err := store.db.Begin()
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			
			for _, event := range payload.Events {
				if _, err := tx.Exec(`INSERT INTO events(ts, count, received_ts) VALUES(?, ?, ?)`,
					event.Timestamp, event.Count, time.Now().Unix()); err != nil {
					tx.Rollback()
					respondError(w, http.StatusInternalServerError, err)
					return
				}
			}
			
			if err := tx.Commit(); err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			
			respondJSON(w, map[string]interface{}{
				"status": "success",
				"message": "批量插入成功",
				"count": len(payload.Events),
			})
		})

		// 清空所有数据端点
		r.Post("/debug/clear-events", func(w http.ResponseWriter, r *http.Request) {
			_, err := store.db.Exec(`DELETE FROM events`)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			respondJSON(w, map[string]string{"status": "success", "message": "所有数据已清空"})
		})

		// 校准功能 - 重新设置基准值
		r.Post("/calibrate", func(w http.ResponseWriter, r *http.Request) {
			var payload struct {
				InitialGas     string `json:"initial_gas"`
				MeterBaseM3    string `json:"meter_base_m3"`
				DesiredMeterM3 string `json:"desired_meter_m3"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				respondError(w, http.StatusBadRequest, err)
				return
			}

			settings, err := loadSettings(store)
			if err != nil {
				respondError(w, http.StatusInternalServerError, fmt.Errorf("加载设置失败: %v", err))
				return
			}

			totalPulses, err := calcTotalPulsesByDelta(store)
			if err != nil {
				respondError(w, http.StatusInternalServerError, fmt.Errorf("计算总脉冲数失败: %v", err))
				return
			}

			// 以当前总脉冲作为新的基准点，后续用气量都从此处开始累积
			if err := store.SetSetting("initial_gas_base_pulses", fmt.Sprintf("%d", totalPulses)); err != nil {
				respondError(w, http.StatusInternalServerError, fmt.Errorf("更新基准脉冲失败: %v", err))
				return
			}
			// 额外记录校准时的基准脉冲，compute 时直接使用
			if err := store.SetSetting("calibrate_base_pulses", fmt.Sprintf("%d", totalPulses)); err != nil {
				respondError(w, http.StatusInternalServerError, fmt.Errorf("保存校准基准脉冲失败: %v", err))
				return
			}

			// 更新基准剩余气量
			baseGasDecimal := parseDecimal(settings.InitialGas, defaultInitialGas)
			if payload.InitialGas != "" {
				baseGasDecimal = parseDecimal(payload.InitialGas, defaultInitialGas)
				if err := store.SetSetting("initial_gas", payload.InitialGas); err != nil {
					respondError(w, http.StatusInternalServerError, fmt.Errorf("更新初始燃气量失败: %v", err))
					return
				}
			}

			if payload.MeterBaseM3 != "" {
				if err := store.SetSetting("meter_base_m3", payload.MeterBaseM3); err != nil {
					respondError(w, http.StatusInternalServerError, fmt.Errorf("更新表盘基准读数失败: %v", err))
					return
				}
			}

			if payload.DesiredMeterM3 != "" {
				if err := store.SetSetting("desired_meter_m3", payload.DesiredMeterM3); err != nil {
					respondError(w, http.StatusInternalServerError, fmt.Errorf("更新目标表盘读数失败: %v", err))
					return
				}
			}

			// 如果没有指定 desired_meter_m3，则使用 meter_base_m3
			if payload.DesiredMeterM3 == "" && payload.MeterBaseM3 != "" {
				if err := store.SetSetting("desired_meter_m3", payload.MeterBaseM3); err != nil {
					respondError(w, http.StatusInternalServerError, fmt.Errorf("同步目标读数失败: %v", err))
					return
				}
			}

			// 保存校准时的基准剩余气量
			if err := store.SetSetting("calibrate_base_gas", baseGasDecimal.String()); err != nil {
				respondError(w, http.StatusInternalServerError, fmt.Errorf("保存校准基准用气量失败: %v", err))
				return
			}

			// 保存校准时间
			if err := store.SetSetting("calibrate_time", fmt.Sprintf("%d", time.Now().Unix())); err != nil {
				respondError(w, http.StatusInternalServerError, fmt.Errorf("保存校准时间失败: %v", err))
				return
			}

			respondJSON(w, map[string]string{
				"status": "success",
				"message": "校准完成",
				"info": fmt.Sprintf("校准基准已设置：基准脉冲=%d，基准剩余气量=%s", totalPulses, baseGasDecimal.String()),
			})
		})

		r.Post("/notify/test", func(w http.ResponseWriter, r *http.Request) {
			settings, err := loadSettings(store)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			
			// 检查 Telegram 配置是否完整
			if settings.TGBotToken == "" || settings.TGChatID == "" {
				var missingFields []string
				if settings.TGBotToken == "" {
					missingFields = append(missingFields, "Bot Token")
				}
				if settings.TGChatID == "" {
					missingFields = append(missingFields, "Chat ID")
				}
				
				respondError(w, http.StatusBadRequest, fmt.Errorf("telegram not configured. Missing: %s", missingFields))
				return
			}
			
			// 检查是否启用了 Telegram 通知
			if !settings.TGEnabled {
				respondError(w, http.StatusBadRequest, fmt.Errorf("telegram notification is disabled. Please enable it first."))
				return
			}
			
			msg := fmt.Sprintf("🧪 <b>测试通知</b>\n\n这是一条测试消息，用于验证Telegram通知配置是否正确。\n\n⏰ 测试时间：%s", time.Now().In(localTZ).Format("2006-01-02 15:04:05"))
			if err := sendTelegramNotification(settings.TGBotToken, settings.TGChatID, msg, settings.TGAPIEndpoint); err != nil {
				respondError(w, http.StatusBadRequest, fmt.Errorf("failed to send telegram notification: %v", err))
				return
			}
			respondJSON(w, map[string]string{"status": "sent", "message": "测试通知已发送"})
		})
	})

	addr := getenv("GAS_SERVER_ADDR", ":8080")
	log.Printf("server listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func respondJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func respondError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func computeMetrics(store *Store) (Metrics, error) {
	settings, err := loadSettings(store)
	if err != nil {
		return Metrics{}, err
	}
	gasPerPulse := parseDecimal(settings.GasPerPulse, defaultGasPerPulse)
	initialGas := parseDecimal(settings.InitialGas, defaultInitialGas)

	now := time.Now().In(localTZ)
	todayStart := startOfDay(now)
	weekStart := startOfWeek(now)
	monthStart := startOfMonth(now)

	todayPulses, err := calcUsagePulsesByDelta(store, todayStart, now)
	if err != nil {
		return Metrics{}, err
	}
	weekPulses, err := calcUsagePulsesByDelta(store, weekStart, now)
	if err != nil {
		return Metrics{}, err
	}
	monthPulses, err := calcUsagePulsesByDelta(store, monthStart, now)
	if err != nil {
		return Metrics{}, err
	}
	totalPulses, err := calcTotalPulsesByDelta(store)
	if err != nil {
		return Metrics{}, err
	}

	// 获取校准时间
	calibrateTimeStr, _ := store.GetSetting("calibrate_time", "0")
	calibrateTime, _ := strconv.ParseInt(calibrateTimeStr, 10, 64)

	// 基准剩余气量：默认使用 initial_gas；如果已校准且存在 calibrate_base_gas，则覆盖
	baseGas := initialGas
	if calibrateTime > 0 {
		if baseGasStr, err := store.GetSetting("calibrate_base_gas", baseGas.String()); err == nil {
			baseGas = parseDecimal(baseGasStr, baseGas.String())
		}
	}
	
	// 基准脉冲：优先使用校准时记录的脉冲，没有则回退到 initial_base_pulses
	basePulses := settings.InitialBasePulses
	if calibrateTime > 0 {
		if basePulsesStr, err := store.GetSetting("calibrate_base_pulses", fmt.Sprintf("%d", basePulses)); err == nil {
			if v, err := strconv.ParseInt(basePulsesStr, 10, 64); err == nil {
				basePulses = v
			}
		}
	}

	// 计算从基准点开始的用气量
	usedSinceBase := totalPulses - basePulses
	if usedSinceBase < 0 {
		usedSinceBase = 0
	}
	usedSinceBaseGas := quantize3(pulsesToGas(usedSinceBase, gasPerPulse))
	
	// 获取校准后的燃气表读数基准
	desiredMeter := parseDecimal(settings.DesiredMeterM3, defaultMeterBase)
	
	// 燃气表读数与剩余气量（均基于当前基准脉冲和基准剩余气量）
	meterReading := quantize3(desiredMeter.Add(usedSinceBaseGas))
	remain := quantize3(baseGas.Sub(usedSinceBaseGas))

	mqttStatus, _ := store.GetSetting("mqtt_status", "not_started")
	lastMsgTS, _ := store.GetSetting("last_msg_ts", "")
	lastMsgTime := ""
	if lastMsgTS != "" {
		if ts, err := strconv.ParseInt(lastMsgTS, 10, 64); err == nil {
			lastMsgTime = time.Unix(ts, 0).In(localTZ).Format("2006-01-02 15:04:05")
		}
	}

	metrics := Metrics{
		TodayGas:     quantize3(pulsesToGas(todayPulses, gasPerPulse)).StringFixed(3),
		WeekGas:      quantize3(pulsesToGas(weekPulses, gasPerPulse)).StringFixed(3),
		MonthGas:     quantize3(pulsesToGas(monthPulses, gasPerPulse)).StringFixed(3),
		TotalUsedGas: quantize3(pulsesToGas(totalPulses, gasPerPulse)).StringFixed(3),
		MeterReading: meterReading.StringFixed(3),
		RemainGas:    remain.StringFixed(3),
		MQTTStatus:   mqttStatus,
		LastMsgTime:  lastMsgTime,
	}

	checkAndNotifyLowGas(store, settings, remain)

	return metrics, nil
}

func saveSettings(store *Store, payload Settings) error {
	if err := store.SetSetting("gas_per_pulse", payload.GasPerPulse); err != nil {
		return err
	}
	if err := store.SetSetting("initial_gas", payload.InitialGas); err != nil {
		return err
	}
	if err := store.SetSetting("initial_gas_base_pulses", fmt.Sprintf("%d", payload.InitialBasePulses)); err != nil {
		return err
	}
	if err := store.SetSetting("meter_base_m3", payload.MeterBaseM3); err != nil {
		return err
	}
	if err := store.SetSetting("desired_meter_m3", payload.DesiredMeterM3); err != nil {
		return err
	}
	if err := store.SetSetting("mqtt_host", payload.MQTTHost); err != nil {
		return err
	}
	if err := store.SetSetting("mqtt_port", fmt.Sprintf("%d", payload.MQTTPort)); err != nil {
		return err
	}
	if err := store.SetSetting("mqtt_user", payload.MQTTUser); err != nil {
		return err
	}
	if err := store.SetSetting("mqtt_pass", payload.MQTTPassword); err != nil {
		return err
	}
	if err := store.SetSetting("mqtt_topic", payload.MQTTTopic); err != nil {
		return err
	}
	if err := store.SetSetting("mqtt_tls", boolToString(payload.MQTTTLS)); err != nil {
		return err
	}
	if err := store.SetSetting("mqtt_tls_insecure", boolToString(payload.MQTTTLSInsecure)); err != nil {
		return err
	}
	if err := store.SetSetting("tg_notify_enabled", boolToString(payload.TGEnabled)); err != nil {
		return err
	}
	if err := store.SetSetting("tg_bot_token", payload.TGBotToken); err != nil {
		return err
	}
	if err := store.SetSetting("tg_chat_id", payload.TGChatID); err != nil {
		return err
	}
	if err := store.SetSetting("tg_api_endpoint", payload.TGAPIEndpoint); err != nil {
		return err
	}
	if err := store.SetSetting("tg_threshold", payload.TGThreshold); err != nil {
		return err
	}
	if err := store.SetSetting("tg_notify_times", fmt.Sprintf("%d", payload.TGNotifyTimes)); err != nil {
		return err
	}
	if err := store.SetSetting("tg_notify_interval_hours", payload.TGNotifyIntervalHour); err != nil {
		return err
	}

	return nil
}

func boolToString(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
