package edge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

type fakePluginConfigRepo struct {
	rows map[string]*model.PluginConfig
}

func newFakePluginConfigRepo() *fakePluginConfigRepo {
	return &fakePluginConfigRepo{rows: map[string]*model.PluginConfig{}}
}

func (r *fakePluginConfigRepo) ListByEdge(_ context.Context, edgeID uint64) ([]*model.PluginConfig, error) {
	out := []*model.PluginConfig{}
	for _, row := range r.rows {
		if row.EdgeID == edgeID {
			cp := *row
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakePluginConfigRepo) Get(_ context.Context, edgeID uint64, plugin string) (*model.PluginConfig, error) {
	row := r.rows[plugin]
	if row == nil || row.EdgeID != edgeID {
		return nil, nil
	}
	cp := *row
	return &cp, nil
}

func (r *fakePluginConfigRepo) Upsert(_ context.Context, in *model.PluginConfig) (*model.PluginConfig, error) {
	cp := *in
	cp.ID = 1
	r.rows[in.PluginName] = &cp
	return &cp, nil
}

func (r *fakePluginConfigRepo) Delete(_ context.Context, _ uint64, plugin string) error {
	delete(r.rows, plugin)
	return nil
}

func (r *fakePluginConfigRepo) CountByPlugin(_ context.Context) (map[string]int64, error) {
	return map[string]int64{}, nil
}

type fakeEndpointResolver struct{}

func (fakeEndpointResolver) Endpoint(_ context.Context, plugin string) string {
	return "http://manager/" + plugin
}

type fakeDatabaseSecretWriter struct {
	reqs []tunnel.WriteDatabaseMetricsSecretRequest
}

func (w *fakeDatabaseSecretWriter) WriteDatabaseMetricsSecret(_ context.Context, _ uint64, req tunnel.WriteDatabaseMetricsSecretRequest) error {
	w.reqs = append(w.reqs, req)
	return nil
}

func TestSetDatabaseMetricsWritesSecretAndStripsPassword(t *testing.T) {
	repo := newFakePluginConfigRepo()
	writer := &fakeDatabaseSecretWriter{}
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)
	uc.SetDatabaseMetricsSecretWriter(writer)

	row, err := uc.Set(context.Background(), 7, model.PluginNameDatabaseMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"sources": []interface{}{
				map[string]interface{}{
					"id":             "pg-prod",
					"db_type":        "postgresql",
					"name":           "pg-prod",
					"listen_address": "127.0.0.1:19187",
					"credentials": map[string]interface{}{
						"host":     "127.0.0.1",
						"port":     "15432",
						"username": "u",
						"password": "p-secret",
						"database": "postgres",
						"sslmode":  "disable",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if len(writer.reqs) != 1 {
		t.Fatalf("secret writes = %d, want 1", len(writer.reqs))
	}
	req := writer.reqs[0]
	if req.SourceID != "pg-prod" {
		t.Fatalf("SourceID = %q, want pg-prod", req.SourceID)
	}
	if req.Path != "/var/lib/ongrid-edge/secrets/pg-prod.dsn" {
		t.Fatalf("Path = %q", req.Path)
	}
	if !strings.Contains(req.Content, "postgresql://u:p-secret@127.0.0.1:15432/postgres?sslmode=disable") {
		t.Fatalf("Content = %q", req.Content)
	}
	blob, err := json.Marshal(row.Spec)
	if err != nil {
		t.Fatalf("Marshal(spec) error = %v", err)
	}
	if strings.Contains(string(blob), "p-secret") || strings.Contains(string(blob), "password") {
		t.Fatalf("stored spec leaked password: %s", blob)
	}
	source := row.Spec["sources"].([]interface{})[0].(map[string]interface{})
	storedCredentials := source["credentials"].(map[string]interface{})
	if storedCredentials["host"] != "127.0.0.1" || storedCredentials["port"] != "15432" || storedCredentials["username"] != "u" || storedCredentials["database"] != "postgres" {
		t.Fatalf("stored credentials = %#v", storedCredentials)
	}
	connection := source["connection"].(map[string]interface{})
	if connection["type"] != "managed" || connection["secret_set"] != true {
		t.Fatalf("connection = %#v", connection)
	}
}

func TestSetDatabaseMetricsWritesTLSConfigAndStripsPassword(t *testing.T) {
	repo := newFakePluginConfigRepo()
	writer := &fakeDatabaseSecretWriter{}
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)
	uc.SetDatabaseMetricsSecretWriter(writer)

	row, err := uc.Set(context.Background(), 7, model.PluginNameDatabaseMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"sources": []interface{}{
				map[string]interface{}{
					"id":             "redis-prod",
					"db_type":        "redis",
					"name":           "redis-prod",
					"listen_address": "127.0.0.1:19121",
					"credentials": map[string]interface{}{
						"host":            "127.0.0.1",
						"port":            "16379",
						"password":        "redis-secret",
						"database":        "0",
						"tls_enabled":     "true",
						"tls_skip_verify": true,
						"tls_ca_file":     "/etc/ongrid-edge/certs/ca.crt",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if len(writer.reqs) != 1 {
		t.Fatalf("secret writes = %d, want 1", len(writer.reqs))
	}
	req := writer.reqs[0]
	if !strings.Contains(req.Content, "rediss://:redis-secret@127.0.0.1:16379/0") {
		t.Fatalf("Content = %q", req.Content)
	}
	blob, err := json.Marshal(row.Spec)
	if err != nil {
		t.Fatalf("Marshal(spec) error = %v", err)
	}
	if strings.Contains(string(blob), "redis-secret") || strings.Contains(string(blob), "password") {
		t.Fatalf("stored spec leaked password: %s", blob)
	}
	source := row.Spec["sources"].([]interface{})[0].(map[string]interface{})
	storedCredentials := source["credentials"].(map[string]interface{})
	if storedCredentials["host"] != "127.0.0.1" || storedCredentials["port"] != "16379" || storedCredentials["database"] != "0" {
		t.Fatalf("stored credentials = %#v", storedCredentials)
	}
	if storedCredentials["tls_enabled"] != true || storedCredentials["tls_skip_verify"] != true {
		t.Fatalf("stored tls credentials = %#v", storedCredentials)
	}
	if _, ok := storedCredentials["tls_ca_file"]; ok {
		t.Fatalf("stored tls credentials kept ca_file while skip_verify=true: %#v", storedCredentials)
	}
	tlsConfig := source["tls"].(map[string]interface{})
	if tlsConfig["enabled"] != true || tlsConfig["skip_verify"] != true {
		t.Fatalf("tls config = %#v", tlsConfig)
	}
	if _, ok := tlsConfig["ca_file"]; ok {
		t.Fatalf("tls config kept ca_file while skip_verify=true: %#v", tlsConfig)
	}
}

func TestSetDatabaseMetricsSkipVerifyNormalizesPostgresSSLMode(t *testing.T) {
	repo := newFakePluginConfigRepo()
	writer := &fakeDatabaseSecretWriter{}
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)
	uc.SetDatabaseMetricsSecretWriter(writer)

	row, err := uc.Set(context.Background(), 7, model.PluginNameDatabaseMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"sources": []interface{}{
				map[string]interface{}{
					"id":             "pg-prod",
					"db_type":        "postgresql",
					"listen_address": "127.0.0.1:19187",
					"credentials": map[string]interface{}{
						"host":            "127.0.0.1",
						"port":            "15432",
						"username":        "u",
						"password":        "p-secret",
						"database":        "postgres",
						"sslmode":         "verify-full",
						"tls_enabled":     true,
						"tls_skip_verify": true,
						"tls_ca_file":     "/etc/ongrid-edge/certs/ca.crt",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if len(writer.reqs) != 1 {
		t.Fatalf("secret writes = %d, want 1", len(writer.reqs))
	}
	if !strings.Contains(writer.reqs[0].Content, "sslmode=require") {
		t.Fatalf("Content = %q, want sslmode=require", writer.reqs[0].Content)
	}
	if strings.Contains(writer.reqs[0].Content, "sslrootcert") {
		t.Fatalf("Content kept sslrootcert while skip_verify=true: %q", writer.reqs[0].Content)
	}
	source := row.Spec["sources"].([]interface{})[0].(map[string]interface{})
	storedCredentials := source["credentials"].(map[string]interface{})
	if storedCredentials["sslmode"] != "require" {
		t.Fatalf("stored sslmode = %#v, want require", storedCredentials["sslmode"])
	}
	if _, ok := storedCredentials["tls_ca_file"]; ok {
		t.Fatalf("stored tls credentials kept ca_file while skip_verify=true: %#v", storedCredentials)
	}
}

func TestSetDatabaseMetricsKeepsVisibleCredentialsAndPreservesPassword(t *testing.T) {
	repo := newFakePluginConfigRepo()
	writer := &fakeDatabaseSecretWriter{}
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)
	uc.SetDatabaseMetricsSecretWriter(writer)

	row, err := uc.Set(context.Background(), 7, model.PluginNameDatabaseMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"sources": []interface{}{
				map[string]interface{}{
					"id":             "pg-prod",
					"db_type":        "postgresql",
					"listen_address": "127.0.0.1:19187",
					"connection": map[string]interface{}{
						"type":       "managed",
						"secret_set": true,
					},
					"credentials": map[string]interface{}{
						"host":     "127.0.0.1",
						"port":     "15432",
						"username": "u",
						"database": "postgres",
						"sslmode":  "verify-full",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if len(writer.reqs) != 1 {
		t.Fatalf("secret writes = %d, want 1", len(writer.reqs))
	}
	req := writer.reqs[0]
	if !req.PreservePassword {
		t.Fatalf("PreservePassword = false, want true")
	}
	if req.Content != "" {
		t.Fatalf("Content = %q, want empty preserve patch", req.Content)
	}
	if req.DBType != "postgresql" || req.Path != "/var/lib/ongrid-edge/secrets/pg-prod.dsn" {
		t.Fatalf("req = %#v", req)
	}
	if req.Credentials["password"] != nil {
		t.Fatalf("preserve req leaked password: %#v", req.Credentials)
	}
	source := row.Spec["sources"].([]interface{})[0].(map[string]interface{})
	storedCredentials := source["credentials"].(map[string]interface{})
	if storedCredentials["username"] != "u" || storedCredentials["sslmode"] != "verify-full" {
		t.Fatalf("stored credentials = %#v", storedCredentials)
	}
}

func TestBuildDatabaseMetricsSecretIncludesDBTypeTLS(t *testing.T) {
	tests := []struct {
		name        string
		dbType      string
		credentials map[string]interface{}
		want        []string
	}{
		{
			name:   "mysql",
			dbType: "mysql",
			credentials: map[string]interface{}{
				"host":            "127.0.0.1",
				"port":            "13306",
				"username":        "root",
				"password":        "mysql-secret",
				"tls_enabled":     true,
				"tls_skip_verify": true,
				"tls_ca_file":     "/etc/ongrid-edge/certs/ca.crt",
				"tls_cert_file":   "/etc/ongrid-edge/certs/client.crt",
				"tls_key_file":    "/etc/ongrid-edge/certs/client.key",
			},
			want: []string{
				"tls=skip-verify",
			},
		},
		{
			name:   "postgresql",
			dbType: "postgresql",
			credentials: map[string]interface{}{
				"host":          "127.0.0.1",
				"port":          "15432",
				"username":      "postgres",
				"password":      "pg-secret",
				"database":      "postgres",
				"tls_enabled":   true,
				"tls_ca_file":   "/etc/ongrid-edge/certs/ca.crt",
				"tls_cert_file": "/etc/ongrid-edge/certs/client.crt",
				"tls_key_file":  "/etc/ongrid-edge/certs/client.key",
			},
			want: []string{
				"sslmode=require",
				"sslrootcert=%2Fetc%2Fongrid-edge%2Fcerts%2Fca.crt",
				"sslcert=%2Fetc%2Fongrid-edge%2Fcerts%2Fclient.crt",
				"sslkey=%2Fetc%2Fongrid-edge%2Fcerts%2Fclient.key",
			},
		},
		{
			name:   "mongodb",
			dbType: "mongodb",
			credentials: map[string]interface{}{
				"host":            "127.0.0.1",
				"port":            "27017",
				"username":        "mongo",
				"password":        "mongo-secret",
				"database":        "admin",
				"auth_source":     "admin",
				"tls_enabled":     true,
				"tls_skip_verify": true,
				"tls_ca_file":     "/etc/ongrid-edge/certs/ca.crt",
				"tls_cert_file":   "/etc/ongrid-edge/certs/client.pem",
			},
			want: []string{
				"tls=true",
				"tlsInsecure=true",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildDatabaseMetricsSecret(tt.dbType, tt.credentials)
			if err != nil {
				t.Fatalf("buildDatabaseMetricsSecret() error = %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("secret = %q, want substring %q", got, want)
				}
			}
			if boolValueForTest(tt.credentials["tls_skip_verify"]) {
				for _, unwanted := range []string{"ssl-ca=", "ssl-cert=", "ssl-key=", "sslrootcert=", "sslcert=", "sslkey=", "tlsCAFile=", "tlsCertificateKeyFile="} {
					if strings.Contains(got, unwanted) {
						t.Fatalf("secret = %q, should not contain %q when skip_verify=true", got, unwanted)
					}
				}
			}
		})
	}
}

func boolValueForTest(v interface{}) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true"
	default:
		return false
	}
}

func intValueForTest(v interface{}) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return 0
	}
}

func TestBuildDatabaseMetricsTLSRejectsRelativePath(t *testing.T) {
	_, err := buildDatabaseMetricsSecret("postgresql", map[string]interface{}{
		"host":        "127.0.0.1",
		"username":    "postgres",
		"password":    "pg-secret",
		"tls_enabled": true,
		"tls_ca_file": "relative/ca.crt",
	})
	if err == nil {
		t.Fatal("buildDatabaseMetricsSecret() error = nil, want invalid path error")
	}
	if !strings.Contains(err.Error(), "absolute edge-local path") {
		t.Fatalf("buildDatabaseMetricsSecret() error = %v", err)
	}
}

func TestSetDatabaseMetricsRejectsRelativeTLSPathWithoutCredentials(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	_, err := uc.Set(context.Background(), 7, model.PluginNameDatabaseMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"sources": []interface{}{
				map[string]interface{}{
					"id":      "pg-prod",
					"db_type": "postgresql",
					"connection": map[string]interface{}{
						"type":       "managed",
						"secret_set": true,
					},
					"tls": map[string]interface{}{
						"enabled": true,
						"ca_file": "relative/ca.crt",
					},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("Set() error = nil, want invalid tls path error")
	}
	if !strings.Contains(err.Error(), "absolute edge-local path") {
		t.Fatalf("Set() error = %v", err)
	}
}

func TestSetDatabaseMetricsRejectsDuplicateListenPort(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	_, err := uc.Set(context.Background(), 7, model.PluginNameDatabaseMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"sources": []interface{}{
				databaseMetricsTestSource("mysql-prod", "mysql", "127.0.0.1:19104"),
				databaseMetricsTestSource("mysql-copy", "mysql", "0.0.0.0:19104"),
			},
		},
	})
	if err == nil {
		t.Fatal("Set() error = nil, want duplicate listen port error")
	}
	if !strings.Contains(err.Error(), "port 19104 conflicts with source") {
		t.Fatalf("Set() error = %v", err)
	}
	if repo.rows[model.PluginNameDatabaseMetrics] != nil {
		t.Fatal("databasemetrics row was persisted after validation error")
	}
}

func TestSetDatabaseMetricsRejectsReservedListenPort(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	_, err := uc.Set(context.Background(), 7, model.PluginNameDatabaseMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"sources": []interface{}{
				databaseMetricsTestSource("mysql-prod", "mysql", "127.0.0.1:9102"),
			},
		},
	})
	if err == nil {
		t.Fatal("Set() error = nil, want reserved listen port error")
	}
	if !strings.Contains(err.Error(), "port 9102 conflicts with hostmetrics") {
		t.Fatalf("Set() error = %v", err)
	}
}

func TestSetDatabaseMetricsKeepsMongoDBExporterCollectors(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	row, err := uc.Set(context.Background(), 7, model.PluginNameDatabaseMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"sources": []interface{}{
				map[string]interface{}{
					"id":             "mongo-prod",
					"db_type":        "mongodb",
					"listen_address": "127.0.0.1:19216",
					"connection": map[string]interface{}{
						"type":       "managed",
						"secret_set": true,
					},
					"exporter": map[string]interface{}{
						"collectors": []interface{}{"diagnosticdata", "fcv", "dbstats"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	source := row.Spec["sources"].([]interface{})[0].(map[string]interface{})
	exporter := source["exporter"].(map[string]interface{})
	collectorsRaw := exporter["collectors"].([]interface{})
	collectors := make([]string, 0, len(collectorsRaw))
	for _, item := range collectorsRaw {
		collectors = append(collectors, item.(string))
	}
	want := []string{"diagnosticdata", "fcv", "dbstats"}
	if strings.Join(collectors, ",") != strings.Join(want, ",") {
		t.Fatalf("collectors = %#v, want %#v", collectors, want)
	}
}

func TestSetDatabaseMetricsKeepsAdvancedExporterOptionsForFourDBs(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	row, err := uc.Set(context.Background(), 7, model.PluginNameDatabaseMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"sources": []interface{}{
				map[string]interface{}{
					"id":             "mysql-prod",
					"db_type":        "mysql",
					"listen_address": "127.0.0.1:19104",
					"connection":     map[string]interface{}{"type": "managed", "secret_set": true},
					"exporter": map[string]interface{}{
						"collectors":                         []interface{}{"perf_schema.replication_group_members", "perf_schema.eventsstatements"},
						"info_schema_tables_databases":       "*",
						"perf_schema_eventsstatements_limit": 50,
					},
				},
				map[string]interface{}{
					"id":             "pg-prod",
					"db_type":        "postgresql",
					"listen_address": "127.0.0.1:19187",
					"connection":     map[string]interface{}{"type": "managed", "secret_set": true},
					"exporter": map[string]interface{}{
						"auto_discover_databases": true,
						"extend_query_path":       "/etc/ongrid-edge/postgres-queries.yaml",
						"include_databases":       []interface{}{"app", "billing"},
					},
				},
				map[string]interface{}{
					"id":             "redis-prod",
					"db_type":        "redis",
					"listen_address": "127.0.0.1:19121",
					"connection":     map[string]interface{}{"type": "managed", "secret_set": true},
					"exporter": map[string]interface{}{
						"is_cluster":                 true,
						"include_sentinel_peer_info": true,
						"check_keys":                 "session:*",
						"check_keys_batch_size":      2000,
					},
				},
				map[string]interface{}{
					"id":             "mongo-prod",
					"db_type":        "mongodb",
					"listen_address": "127.0.0.1:19216",
					"connection":     map[string]interface{}{"type": "managed", "secret_set": true},
					"exporter": map[string]interface{}{
						"collectors":       []interface{}{"diagnosticdata", "shards"},
						"discovering_mode": true,
						"split_cluster":    true,
						"collstats_limit":  200,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	sources := row.Spec["sources"].([]interface{})
	mysqlExporter := sources[0].(map[string]interface{})["exporter"].(map[string]interface{})
	if mysqlExporter["info_schema_tables_databases"] != "*" || intValueForTest(mysqlExporter["perf_schema_eventsstatements_limit"]) != 50 {
		t.Fatalf("mysql exporter = %#v", mysqlExporter)
	}
	pgExporter := sources[1].(map[string]interface{})["exporter"].(map[string]interface{})
	if pgExporter["auto_discover_databases"] != true || pgExporter["extend_query_path"] != "/etc/ongrid-edge/postgres-queries.yaml" {
		t.Fatalf("postgres exporter = %#v", pgExporter)
	}
	redisExporter := sources[2].(map[string]interface{})["exporter"].(map[string]interface{})
	if redisExporter["is_cluster"] != true || intValueForTest(redisExporter["check_keys_batch_size"]) != 2000 {
		t.Fatalf("redis exporter = %#v", redisExporter)
	}
	mongoExporter := sources[3].(map[string]interface{})["exporter"].(map[string]interface{})
	if mongoExporter["discovering_mode"] != true || intValueForTest(mongoExporter["collstats_limit"]) != 200 {
		t.Fatalf("mongo exporter = %#v", mongoExporter)
	}
}

func TestSetDatabaseMetricsRejectsUnsupportedMongoDBExporterCollector(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	_, err := uc.Set(context.Background(), 7, model.PluginNameDatabaseMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"sources": []interface{}{
				map[string]interface{}{
					"id":             "mongo-prod",
					"db_type":        "mongodb",
					"listen_address": "127.0.0.1:19216",
					"connection": map[string]interface{}{
						"type":       "managed",
						"secret_set": true,
					},
					"exporter": map[string]interface{}{
						"collectors": []interface{}{"collect-all"},
					},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("Set() error = nil, want unsupported collector error")
	}
	if !strings.Contains(err.Error(), `exporter.collectors unsupported "collect-all"`) {
		t.Fatalf("Set() error = %v", err)
	}
}

func TestSetDatabaseMetricsRejectsWrongExporterFieldForDBType(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	_, err := uc.Set(context.Background(), 7, model.PluginNameDatabaseMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"sources": []interface{}{
				map[string]interface{}{
					"id":             "redis-prod",
					"db_type":        "redis",
					"listen_address": "127.0.0.1:19121",
					"connection":     map[string]interface{}{"type": "managed", "secret_set": true},
					"exporter": map[string]interface{}{
						"collectors": []interface{}{"shards"},
					},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("Set() error = nil, want unsupported redis collector error")
	}
	if !strings.Contains(err.Error(), `exporter.collectors unsupported "shards" for redis`) {
		t.Fatalf("Set() error = %v", err)
	}
}

func TestSetDatabaseMetricsRejectsRelativeExporterPath(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	_, err := uc.Set(context.Background(), 7, model.PluginNameDatabaseMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"sources": []interface{}{
				map[string]interface{}{
					"id":             "pg-prod",
					"db_type":        "postgresql",
					"listen_address": "127.0.0.1:19187",
					"connection":     map[string]interface{}{"type": "managed", "secret_set": true},
					"exporter": map[string]interface{}{
						"extend_query_path": "queries.yaml",
					},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("Set() error = nil, want relative path error")
	}
	if !strings.Contains(err.Error(), "must be an absolute edge-local path") {
		t.Fatalf("Set() error = %v", err)
	}
}

func databaseMetricsTestSource(id, dbType, listenAddress string) map[string]interface{} {
	return map[string]interface{}{
		"id":             id,
		"db_type":        dbType,
		"listen_address": listenAddress,
		"connection": map[string]interface{}{
			"type":       "managed",
			"secret_set": true,
		},
	}
}
