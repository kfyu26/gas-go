package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
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
	defer worker.Stop()

	templateDir, staticDir := resolveAssetDirs()
	indexTmpl := mustParseTemplate(filepath.Join(templateDir, "index.html"))
	loginTmpl := mustParseTemplate(filepath.Join(templateDir, "login.html"))
	dataImportTmpl := mustParseTemplate(filepath.Join(templateDir, "data-import.html"))

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(AuthMiddleware(store))

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, indexTmpl, nil)
	})

	r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, loginTmpl, nil)
	})

	r.Get("/data-import", func(w http.ResponseWriter, r *http.Request) {
		enabled, _ := isAuthEnabled(store)
		configured, _ := isAdminConfigured(store)
		if enabled && !configured {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		renderTemplate(w, dataImportTmpl, nil)
	})

	r.Route("/api", func(r chi.Router) {
		// 登录
		r.Post("/login", func(w http.ResponseWriter, r *http.Request) {
			var payload struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				respondError(w, http.StatusBadRequest, err)
				return
			}

			configured, err := isAdminConfigured(store)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}

			if !configured {
				if payload.Username == "" || payload.Password == "" {
					respondError(w, http.StatusBadRequest, fmt.Errorf("请输入用户名和密码"))
					return
				}

				initialPassword := os.Getenv("GAS_ADMIN_PASSWORD")
				if initialPassword == "" {
					if err := InitAdmin(store, payload.Username, payload.Password); err != nil {
						respondError(w, http.StatusInternalServerError, err)
						return
					}
				} else {
					if err := InitAdmin(store, payload.Username, initialPassword); err != nil {
						respondError(w, http.StatusInternalServerError, err)
						return
					}
				}

				token, err := GenerateToken(store)
				if err != nil {
					respondError(w, http.StatusInternalServerError, err)
					return
				}

				respondJSON(w, map[string]string{
					"status":  "success",
					"message": "管理员账号初始化成功",
					"token":   token,
				})
				return
			}

			valid, err := VerifyAdminPassword(store, payload.Password)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			if !valid {
				respondError(w, http.StatusUnauthorized, fmt.Errorf("密码错误"))
				return
			}

			adminUsername, _ := store.GetSetting("admin_username", "admin")
			if payload.Username != "" && payload.Username != adminUsername {
				respondError(w, http.StatusUnauthorized, fmt.Errorf("用户名错误"))
				return
			}

			token, err := GenerateToken(store)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}

			http.SetCookie(w, &http.Cookie{
				Name:     "auth_token",
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				Expires:  time.Now().Add(time.Hour * tokenExpiryHours),
			})

			respondJSON(w, map[string]string{
				"status": "success",
				"token":  token,
			})
		})

		// 更新管理员账号/密码
		r.Post("/admin/update", func(w http.ResponseWriter, r *http.Request) {
			var payload struct {
				CurrentPassword string `json:"current_password"`
				NewUsername     string `json:"new_username"`
				NewPassword     string `json:"new_password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				respondError(w, http.StatusBadRequest, err)
				return
			}

			configured, err := isAdminConfigured(store)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			if !configured {
				respondError(w, http.StatusBadRequest, fmt.Errorf("尚未设置管理员，请先创建管理员"))
				return
			}
			if payload.CurrentPassword == "" {
				respondError(w, http.StatusBadRequest, fmt.Errorf("请输入当前密码"))
				return
			}
			if payload.NewUsername == "" {
				respondError(w, http.StatusBadRequest, fmt.Errorf("请输入新的管理员账号"))
				return
			}
			if payload.NewPassword == "" {
				respondError(w, http.StatusBadRequest, fmt.Errorf("请输入新的管理员密码"))
				return
			}

			valid, err := VerifyAdminPassword(store, payload.CurrentPassword)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			if !valid {
				respondError(w, http.StatusUnauthorized, fmt.Errorf("当前密码错误"))
				return
			}

			if err := UpdateAdminCredentials(store, payload.NewUsername, payload.NewPassword); err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}

			respondJSON(w, map[string]string{
				"status":  "success",
				"message": "管理员账号已更新，请重新登录",
			})
		})

		// 退出登录
		r.Post("/logout", func(w http.ResponseWriter, r *http.Request) {
			http.SetCookie(w, &http.Cookie{
				Name:     "auth_token",
				Value:    "",
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   -1,
			})
			respondJSON(w, map[string]string{"status": "success"})
		})
		// 认证状态
		r.Get("/auth/status", func(w http.ResponseWriter, r *http.Request) {
			enabled, _ := isAuthEnabled(store)
			configured, _ := isAdminConfigured(store)
			authenticated := false

			if enabled && configured {
				// 优先用 Authorization，其次用 Cookie
				if tokenStr, err := extractToken(r); err == nil {
					if _, err := ValidateToken(tokenStr); err == nil {
						authenticated = true
					}
				}
			}

			respondJSON(w, map[string]interface{}{
				"enabled":       enabled,
				"configured":    configured,
				"authenticated": authenticated,
			})
		})

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
				"settings":     settings,
				"now":          now.Format("2006-01-02 15:04:05"),
				"today_start":  todayStart.Format("2006-01-02 15:04:05"),
				"today_pulses": todayPulses,
				"total_pulses": totalPulses,
				"hourly_data":  hourly,
			})
		})

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
				"status":  "success",
				"message": "批量插入成功",
				"count":   len(payload.Events),
			})
		})

		r.Post("/debug/clear-events", func(w http.ResponseWriter, r *http.Request) {
			_, err := store.db.Exec(`DELETE FROM events`)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			respondJSON(w, map[string]string{"status": "success", "message": "所有数据已清空"})
		})

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
				respondError(w, http.StatusInternalServerError, fmt.Errorf("计算总脉冲失败: %v", err))
				return
			}

			if err := store.SetSetting("initial_gas_base_pulses", fmt.Sprintf("%d", totalPulses)); err != nil {
				respondError(w, http.StatusInternalServerError, fmt.Errorf("更新基准脉冲失败: %v", err))
				return
			}
			if err := store.SetSetting("calibrate_base_pulses", fmt.Sprintf("%d", totalPulses)); err != nil {
				respondError(w, http.StatusInternalServerError, fmt.Errorf("保存校准基准脉冲失败: %v", err))
				return
			}

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
					respondError(w, http.StatusInternalServerError, fmt.Errorf("更新表底数失败: %v", err))
					return
				}
			}

			if payload.DesiredMeterM3 != "" {
				if err := store.SetSetting("desired_meter_m3", payload.DesiredMeterM3); err != nil {
					respondError(w, http.StatusInternalServerError, fmt.Errorf("更新目标读数失败: %v", err))
					return
				}
			}

			if payload.DesiredMeterM3 == "" && payload.MeterBaseM3 != "" {
				if err := store.SetSetting("desired_meter_m3", payload.MeterBaseM3); err != nil {
					respondError(w, http.StatusInternalServerError, fmt.Errorf("同步目标读数失败: %v", err))
					return
				}
			}

			if err := store.SetSetting("calibrate_base_gas", baseGasDecimal.String()); err != nil {
				respondError(w, http.StatusInternalServerError, fmt.Errorf("保存校准基准燃气量失败: %v", err))
				return
			}

			if err := store.SetSetting("calibrate_time", fmt.Sprintf("%d", time.Now().Unix())); err != nil {
				respondError(w, http.StatusInternalServerError, fmt.Errorf("保存校准时间失败: %v", err))
				return
			}

			respondJSON(w, map[string]string{
				"status":  "success",
				"message": "校准完成",
				"info":    fmt.Sprintf("校准基准已设置：基准脉冲=%d，基准燃气量=%s", totalPulses, baseGasDecimal.String()),
			})
		})

		r.Post("/notify/test", func(w http.ResponseWriter, r *http.Request) {
			settings, err := loadSettings(store)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}

			if settings.TGBotToken == "" || settings.TGChatID == "" {
				var missing []string
				if settings.TGBotToken == "" {
					missing = append(missing, "Bot Token")
				}
				if settings.TGChatID == "" {
					missing = append(missing, "Chat ID")
				}
				respondError(w, http.StatusBadRequest, fmt.Errorf("telegram 未配置完整，缺少: %v", missing))
				return
			}

			if !settings.TGEnabled {
				respondError(w, http.StatusBadRequest, fmt.Errorf("telegram 通知未开启"))
				return
			}

			msg := fmt.Sprintf("🧪 <b>测试通知</b>\n\n这是一条测试消息，用于验证 Telegram 通知配置是否正确。\n\n⏰ 发送时间：%s",
				time.Now().In(localTZ).Format("2006-01-02 15:04:05"))
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

func resolveAssetDirs() (templateDir string, staticDir string) {
	templateDir = "templates"
	staticDir = "static"
	// 支持从仓库根目录运行
	if _, err := os.Stat(filepath.Join("gas", "templates", "index.html")); err == nil {
		templateDir = filepath.Join("gas", "templates")
		staticDir = filepath.Join("gas", "static")
	}
	return
}

func mustParseTemplate(path string) *template.Template {
	tmpl, err := template.ParseFiles(path)
	if err != nil {
		log.Fatalf("parse template %s: %v", path, err)
	}
	return tmpl
}

func renderTemplate(w http.ResponseWriter, tmpl *template.Template, data any) {
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

	calibrateTimeStr, _ := store.GetSetting("calibrate_time", "0")
	calibrateTime, _ := strconv.ParseInt(calibrateTimeStr, 10, 64)

	baseGas := initialGas
	if calibrateTime > 0 {
		if baseGasStr, err := store.GetSetting("calibrate_base_gas", baseGas.String()); err == nil {
			baseGas = parseDecimal(baseGasStr, baseGas.String())
		}
	}

	basePulses := settings.InitialBasePulses
	if calibrateTime > 0 {
		if basePulsesStr, err := store.GetSetting("calibrate_base_pulses", fmt.Sprintf("%d", basePulses)); err == nil {
			if v, err := strconv.ParseInt(basePulsesStr, 10, 64); err == nil {
				basePulses = v
			}
		}
	}

	usedSinceBase := totalPulses - basePulses
	if usedSinceBase < 0 {
		usedSinceBase = 0
	}
	usedSinceBaseGas := quantize3(pulsesToGas(usedSinceBase, gasPerPulse))

	desiredMeter := parseDecimal(settings.DesiredMeterM3, defaultMeterBase)
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
	if err := store.SetSetting("auth_enabled", boolToString(payload.AuthEnabled)); err != nil {
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
