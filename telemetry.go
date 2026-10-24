package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

type TelemetryConfig struct {
	SourcePath       string
	TimestampField   string
	ValueField       string
	TemperatureField string
}

type TelemetryPoint struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	TempC     *float64  `json:"tempC,omitempty"`
}

// ParseTelemetry reads the child records written by Firebase Realtime Database.
// It accepts the firmware's UTC timestamp strings and Unix timestamps in
// seconds or milliseconds, which makes older migrated records readable too.
func ParseTelemetry(raw []byte, config TelemetryConfig) ([]TelemetryPoint, error) {
	var records map[string]json.RawMessage
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("decode telemetry records: %w", err)
	}
	points := make([]TelemetryPoint, 0, len(records))
	for id, record := range records {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(record, &fields); err != nil {
			continue
		}
		value, ok := number(fields[config.ValueField])
		if !ok {
			continue
		}
		timestamp, ok := parseTimestamp(fields[config.TimestampField])
		if !ok {
			continue
		}
		point := TelemetryPoint{ID: id, Timestamp: timestamp, Value: value}
		if temp, ok := number(fields[config.TemperatureField]); ok {
			point.TempC = &temp
		}
		points = append(points, point)
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].Timestamp.Equal(points[j].Timestamp) {
			return points[i].ID < points[j].ID
		}
		return points[i].Timestamp.Before(points[j].Timestamp)
	})
	return points, nil
}

func number(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	return value, err == nil
}

func parseTimestamp(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if value, err := time.Parse(layout, text); err == nil {
				return value.UTC(), true
			}
		}
		return time.Time{}, false
	}
	value, ok := number(raw)
	if !ok {
		return time.Time{}, false
	}
	if value > 1e12 || value < -1e12 {
		return time.UnixMilli(int64(value)).UTC(), true
	}
	return time.Unix(int64(value), 0).UTC(), true
}

func filterRecent(points []TelemetryPoint, now time.Time) []TelemetryPoint {
	cutoff := now.Add(-24 * time.Hour)
	filtered := make([]TelemetryPoint, 0, len(points))
	for _, point := range points {
		if !point.Timestamp.Before(cutoff) && !point.Timestamp.After(now.Add(time.Minute)) {
			filtered = append(filtered, point)
		}
	}
	return filtered
}

func latestByTimestamp(points []TelemetryPoint) (TelemetryPoint, bool) {
	if len(points) == 0 {
		return TelemetryPoint{}, false
	}
	latest := points[0]
	for _, point := range points[1:] {
		if point.Timestamp.After(latest.Timestamp) {
			latest = point
		}
	}
	return latest, true
}
