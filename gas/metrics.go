package main

import (
	"time"

	"github.com/shopspring/decimal"
)

func normalizeDelta(prev *int64, cur int64) int64 {
	if prev == nil {
		return 0
	}
	delta := cur - *prev
	if delta < 0 {
		return cur
	}
	return delta
}

func calcUsagePulsesByDelta(store *Store, start, end time.Time) (int64, error) {
	startTS := start.Unix()
	endTS := end.Unix()

	prev, err := store.FetchPrevCountBefore(startTS)
	if err != nil {
		return 0, err
	}
	rows, err := store.FetchEventsInRange(startTS, endTS)
	if err != nil {
		return 0, err
	}

	var pulses int64
	prevCount := prev
	for _, row := range rows {
		d := normalizeDelta(prevCount, row.Count)
		pulses += d
		c := row.Count
		prevCount = &c
	}
	if pulses < 0 {
		return 0, nil
	}
	return pulses, nil
}

func calcTotalPulsesByDelta(store *Store) (int64, error) {
	rows, err := store.FetchAllEvents()
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	var pulses int64
	var prev *int64
	for _, row := range rows {
		d := normalizeDelta(prev, row.Count)
		pulses += d
		c := row.Count
		prev = &c
	}
	if pulses < 0 {
		return 0, nil
	}
	return pulses, nil
}

func pulsesToGas(pulses int64, gasPerPulse decimal.Decimal) decimal.Decimal {
	return decimal.NewFromInt(pulses).Mul(gasPerPulse)
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func startOfWeek(t time.Time) time.Time {
	sod := startOfDay(t)
	weekday := int(sod.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	return sod.AddDate(0, 0, -(weekday - 1))
}

func startOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

func calcHourlyPulsesToday(store *Store, now time.Time) ([]int64, error) {
	dayStart := startOfDay(now)
	dayEnd := dayStart.Add(24 * time.Hour)

	prev, err := store.FetchPrevCountBefore(dayStart.Unix())
	if err != nil {
		return nil, err
	}
	rows, err := store.FetchEventsInRange(dayStart.Unix(), dayEnd.Unix())
	if err != nil {
		return nil, err
	}

	hourly := make([]int64, 24)
	prevCount := prev
	for _, row := range rows {
		delta := normalizeDelta(prevCount, row.Count)
		hour := time.Unix(row.Timestamp, 0).In(now.Location()).Hour()
		if hour >= 0 && hour < 24 {
			hourly[hour] += delta
		}
		c := row.Count
		prevCount = &c
	}
	return hourly, nil
}

func calcMonthlyPulsesCurrentYear(store *Store, now time.Time) ([]int64, error) {
	monthly := make([]int64, 12)
	for month := 1; month <= 12; month++ {
		monthStart := time.Date(now.Year(), time.Month(month), 1, 0, 0, 0, 0, now.Location())
		var monthEnd time.Time
		if month < 12 {
			monthEnd = time.Date(now.Year(), time.Month(month+1), 1, 0, 0, 0, 0, now.Location())
		} else {
			monthEnd = time.Date(now.Year()+1, time.January, 1, 0, 0, 0, 0, now.Location())
		}
		pulses, err := calcUsagePulsesByDelta(store, monthStart, monthEnd)
		if err != nil {
			return nil, err
		}
		monthly[month-1] = pulses
	}
	return monthly, nil
}