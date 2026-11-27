package portal

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

func TestPortalLoggerUsesUTCPlusEight(t *testing.T) {
	var output bytes.Buffer
	logger := newPortalLogger(&output)
	instant := time.Date(2026, 11, 27, 20, 11, 21, 0, time.UTC)
	record := slog.NewRecord(instant, slog.LevelInfo, "health alert investigation", 0)
	record.AddAttrs(slog.String("source", "zabbix"), slog.Time("record_time", instant))
	if err := logger.Handler().Handle(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Time       string `json:"time"`
		RecordTime string `json:"record_time"`
		Source     string `json:"source"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Time != "2026-11-28T04:11:21+08:00" || result.RecordTime != "2026-11-27T20:11:21Z" || result.Source != "zabbix" {
		t.Fatalf("unexpected log record: %+v", result)
	}
}
