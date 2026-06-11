package custommetrics

import (
	"strings"
	"testing"
)

func TestParseSpecRejectsDuplicateTargetURL(t *testing.T) {
	_, err := parseSpec(map[string]interface{}{
		"targets": []interface{}{
			map[string]interface{}{
				"id":         "mysql-exporter",
				"target_url": "http://127.0.0.1:9104/metrics",
			},
			map[string]interface{}{
				"id":         "mysql-exporter-copy",
				"target_url": "http://127.0.0.1:9104/metrics",
			},
		},
	})
	if err == nil {
		t.Fatal("parseSpec() error = nil, want duplicate target_url error")
	}
	if !strings.Contains(err.Error(), "duplicate target_url") {
		t.Fatalf("parseSpec() error = %v", err)
	}
}
