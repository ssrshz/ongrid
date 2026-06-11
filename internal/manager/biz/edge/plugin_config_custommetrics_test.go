package edge

import (
	"context"
	"strings"
	"testing"

	model "github.com/ongridio/ongrid/internal/manager/model/edge"
)

func TestSetCustomMetricsRejectsDuplicateTargetURL(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	_, err := uc.Set(context.Background(), 7, model.PluginNameCustomMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
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
		},
	})
	if err == nil {
		t.Fatal("Set() error = nil, want duplicate target_url error")
	}
	if !strings.Contains(err.Error(), "duplicate target_url") {
		t.Fatalf("Set() error = %v", err)
	}
	if repo.rows[model.PluginNameCustomMetrics] != nil {
		t.Fatal("custommetrics row was persisted after validation error")
	}
}
