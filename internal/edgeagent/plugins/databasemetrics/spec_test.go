package databasemetrics

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadSecretFileRequiresStrictPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg.dsn")
	if err := os.WriteFile(path, []byte("postgres://user:pass@127.0.0.1:5432/postgres?sslmode=disable\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := readSecretFile(path)
	if err != nil {
		t.Fatalf("readSecretFile() error = %v", err)
	}
	if !strings.HasPrefix(got, "postgres://user:pass@") {
		t.Fatalf("readSecretFile() = %q", got)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	if _, err := readSecretFile(path); err == nil {
		t.Fatal("readSecretFile() error = nil, want permissions error")
	}
}

func TestSourceCommandKeepsSecretsOutOfArgsWhereSupported(t *testing.T) {
	tests := []struct {
		dbType     string
		tls        tlsSpec
		secretPath string
		secret     string
		wantBinary string
		wantArgs   []string
		wantEnv    []string
	}{
		{
			dbType:     "mysql",
			tls:        tlsSpec{SkipVerify: true},
			secretPath: "/etc/ongrid-edge/secrets/mysql.my.cnf",
			secret:     "[client]\nuser=u\npassword=p",
			wantBinary: "/bin/mysqld_exporter",
			wantArgs:   []string{"--web.listen-address=127.0.0.1:19104", "--config.my-cnf=/etc/ongrid-edge/secrets/mysql.my.cnf", "--tls.insecure-skip-verify"},
		},
		{
			dbType:     "postgresql",
			secretPath: "/etc/ongrid-edge/secrets/pg.dsn",
			secret:     "postgres://u:p@127.0.0.1/postgres?sslmode=disable",
			wantBinary: "/bin/postgres_exporter",
			wantArgs:   []string{"--web.listen-address=127.0.0.1:19104"},
			wantEnv:    []string{"DATA_SOURCE_NAME=postgres://u:p@127.0.0.1/postgres?sslmode=disable"},
		},
		{
			dbType:     "redis",
			tls:        tlsSpec{Enabled: true, SkipVerify: true, CAFile: "/etc/ongrid-edge/certs/ca.crt", CertFile: "/etc/ongrid-edge/certs/client.crt", KeyFile: "/etc/ongrid-edge/certs/client.key"},
			secretPath: "/etc/ongrid-edge/secrets/redis.dsn",
			secret:     "rediss://:p@127.0.0.1:6379",
			wantBinary: "/bin/redis_exporter",
			wantArgs:   []string{"--web.listen-address=127.0.0.1:19104", "-skip-tls-verification", "-tls-ca-cert-file=/etc/ongrid-edge/certs/ca.crt", "-tls-client-cert-file=/etc/ongrid-edge/certs/client.crt", "-tls-client-key-file=/etc/ongrid-edge/certs/client.key"},
			wantEnv:    []string{"REDIS_ADDR=rediss://:p@127.0.0.1:6379"},
		},
		{
			dbType:     "mongodb",
			secretPath: "/etc/ongrid-edge/secrets/mongo.dsn",
			secret:     "mongodb://u:p@127.0.0.1:27017/admin",
			wantBinary: "/bin/mongodb_exporter",
			wantArgs: []string{
				"--web.listen-address=127.0.0.1:19104",
				"--collector.diagnosticdata",
				"--collector.replicasetstatus",
				"--collector.fcv",
			},
			wantEnv: []string{"MONGODB_URI=mongodb://u:p@127.0.0.1:27017/admin"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.dbType, func(t *testing.T) {
			src := sourceSpec{DBType: tt.dbType, ListenAddress: "127.0.0.1:19104", TLS: tt.tls}
			binary, args, env, err := src.command("/bin", tt.secretPath, tt.secret)
			if err != nil {
				t.Fatalf("command() error = %v", err)
			}
			if binary != tt.wantBinary {
				t.Fatalf("binary = %q, want %q", binary, tt.wantBinary)
			}
			if !reflect.DeepEqual(args, tt.wantArgs) {
				t.Fatalf("args = %#v, want %#v", args, tt.wantArgs)
			}
			if !reflect.DeepEqual(env, tt.wantEnv) {
				t.Fatalf("env = %#v, want %#v", env, tt.wantEnv)
			}
			for _, arg := range args {
				if strings.Contains(arg, "p@") || strings.Contains(arg, "password=p") {
					t.Fatalf("secret leaked through args: %#v", args)
				}
			}
		})
	}
}

func TestSourceCommandUsesExporterAdvancedOptions(t *testing.T) {
	tests := []struct {
		name       string
		source     sourceSpec
		secretPath string
		secret     string
		wantArgs   []string
	}{
		{
			name:       "mysql collectors and perf schema limits",
			secretPath: "/etc/ongrid-edge/secrets/mysql.my.cnf",
			secret:     "[client]\nuser=u\npassword=p",
			source: sourceSpec{
				DBType:        "mysql",
				ListenAddress: "127.0.0.1:19104",
				Exporter: exporterSpec{
					Collectors: []string{"perf_schema.replication_group_members", "perf_schema.eventsstatements"},
					Strings:    map[string]string{"info_schema_tables_databases": "*"},
					Ints:       map[string]int{"perf_schema_eventsstatements_limit": 50},
				},
			},
			wantArgs: []string{
				"--collect.perf_schema.replication_group_members",
				"--collect.perf_schema.eventsstatements",
				"--collect.info_schema.tables.databases=*",
				"--collect.perf_schema.eventsstatements.limit=50",
			},
		},
		{
			name:       "postgres discovery and custom query path",
			secretPath: "/etc/ongrid-edge/secrets/pg.dsn",
			secret:     "postgres://u:p@127.0.0.1/postgres?sslmode=disable",
			source: sourceSpec{
				DBType:        "postgresql",
				ListenAddress: "127.0.0.1:19187",
				Exporter: exporterSpec{
					Bools:   map[string]bool{"auto_discover_databases": true},
					Strings: map[string]string{"extend_query_path": "/etc/ongrid-edge/postgres-queries.yaml"},
					Lists:   map[string][]string{"include_databases": []string{"app", "billing"}},
				},
			},
			wantArgs: []string{
				"--auto-discover-databases",
				"--extend.query-path=/etc/ongrid-edge/postgres-queries.yaml",
				"--include-databases=app,billing",
			},
		},
		{
			name:       "redis cluster and key scans",
			secretPath: "/etc/ongrid-edge/secrets/redis.dsn",
			secret:     "redis://127.0.0.1:6379/0",
			source: sourceSpec{
				DBType:        "redis",
				ListenAddress: "127.0.0.1:19121",
				Exporter: exporterSpec{
					Bools:   map[string]bool{"is_cluster": true, "include_sentinel_peer_info": true},
					Strings: map[string]string{"check_keys": "session:*", "count_keys": "db0=session:*"},
					Ints:    map[string]int{"check_keys_batch_size": 2000},
				},
			},
			wantArgs: []string{
				"--is-cluster",
				"--include-sentinel-peer-info",
				"--check-keys=session:*",
				"--count-keys=db0=session:*",
				"--check-keys-batch-size=2000",
			},
		},
		{
			name:       "mongodb cluster modes and collector tuning",
			secretPath: "/etc/ongrid-edge/secrets/mongo.dsn",
			secret:     "mongodb://u:p@127.0.0.1:27017/admin",
			source: sourceSpec{
				DBType:        "mongodb",
				ListenAddress: "127.0.0.1:19216",
				Exporter: exporterSpec{
					Collectors:    []string{"diagnosticdata", "shards"},
					CollectorsSet: true,
					Bools:         map[string]bool{"discovering_mode": true, "compatible_mode": true, "split_cluster": true},
					Strings:       map[string]string{"currentopmetrics_slow_time": "3m"},
					Ints:          map[string]int{"collstats_limit": 200, "profile_time_ts": 30},
				},
			},
			wantArgs: []string{
				"--collector.diagnosticdata",
				"--collector.shards",
				"--compatible-mode",
				"--discovering-mode",
				"--split-cluster",
				"--collector.currentopmetrics-slow-time=3m",
				"--collector.collstats-limit=200",
				"--collector.profile-time-ts=30",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, args, _, err := tt.source.command("/bin", tt.secretPath, tt.secret)
			if err != nil {
				t.Fatalf("command() error = %v", err)
			}
			for _, want := range tt.wantArgs {
				if !containsString(args, want) {
					t.Fatalf("args = %#v, missing %q", args, want)
				}
			}
		})
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestParseSourceAcceptsManagedConnection(t *testing.T) {
	source, err := parseSource(0, map[string]interface{}{
		"id":      "mysql-prod",
		"db_type": "mysql",
		"tls": map[string]interface{}{
			"enabled":     true,
			"skip_verify": true,
			"ca_file":     "/etc/ongrid-edge/certs/ca.crt",
		},
		"connection": map[string]interface{}{
			"type": "managed",
			"path": "/var/lib/ongrid-edge/secrets/mysql-prod.my.cnf",
		},
	})
	if err != nil {
		t.Fatalf("parseSource() error = %v", err)
	}
	if source.Connection.Type != "managed" {
		t.Fatalf("connection type = %q, want managed", source.Connection.Type)
	}
	if !source.TLS.Enabled || !source.TLS.SkipVerify || source.TLS.CAFile != "/etc/ongrid-edge/certs/ca.crt" {
		t.Fatalf("TLS = %#v", source.TLS)
	}
}

func TestParseSourceRejectsFileConnection(t *testing.T) {
	_, err := parseSource(0, map[string]interface{}{
		"id":      "mysql-prod",
		"db_type": "mysql",
		"connection": map[string]interface{}{
			"type": "file",
			"path": "/var/lib/ongrid-edge/secrets/mysql-prod.my.cnf",
		},
	})
	if err == nil {
		t.Fatal("parseSource() error = nil, want managed-only error")
	}
	if !strings.Contains(err.Error(), "connection.type must be managed") {
		t.Fatalf("parseSource() error = %v", err)
	}
}

func TestParseSourceUsesDBTypeLabelDropDefaults(t *testing.T) {
	tests := []struct {
		dbType string
		want   []string
	}{
		{dbType: "mysql", want: []string{"query", "statement"}},
		{dbType: "postgresql", want: []string{"query", "statement"}},
		{dbType: "redis", want: nil},
		{dbType: "mongodb", want: []string{"collection", "query"}},
	}
	for _, tt := range tests {
		t.Run(tt.dbType, func(t *testing.T) {
			source, err := parseSource(0, map[string]interface{}{
				"id":      tt.dbType + "-prod",
				"db_type": tt.dbType,
				"connection": map[string]interface{}{
					"type": "managed",
					"path": "/var/lib/ongrid-edge/secrets/" + tt.dbType + "-prod.dsn",
				},
			})
			if err != nil {
				t.Fatalf("parseSource() error = %v", err)
			}
			if !reflect.DeepEqual(source.LabelDrop, tt.want) {
				t.Fatalf("LabelDrop = %#v, want %#v", source.LabelDrop, tt.want)
			}
		})
	}
}

func TestParseSourcePreservesExplicitEmptyLabelDrop(t *testing.T) {
	source, err := parseSource(0, map[string]interface{}{
		"id":         "mysql-prod",
		"db_type":    "mysql",
		"label_drop": []interface{}{},
		"connection": map[string]interface{}{
			"type": "managed",
			"path": "/var/lib/ongrid-edge/secrets/mysql-prod.my.cnf",
		},
	})
	if err != nil {
		t.Fatalf("parseSource() error = %v", err)
	}
	if source.LabelDrop == nil || len(source.LabelDrop) != 0 {
		t.Fatalf("LabelDrop = %#v, want explicit empty slice", source.LabelDrop)
	}
}

func TestParseSourceUsesMongoDBExporterCollectors(t *testing.T) {
	source, err := parseSource(0, map[string]interface{}{
		"id":      "mongo-prod",
		"db_type": "mongodb",
		"connection": map[string]interface{}{
			"type": "managed",
			"path": "/var/lib/ongrid-edge/secrets/mongo-prod.dsn",
		},
		"exporter": map[string]interface{}{
			"collectors": []interface{}{"diagnosticdata", "fcv", "dbstats"},
		},
	})
	if err != nil {
		t.Fatalf("parseSource() error = %v", err)
	}
	binary, args, _, err := source.command("/bin", source.Connection.Path, "mongodb://u:p@127.0.0.1:27017/admin")
	if err != nil {
		t.Fatalf("command() error = %v", err)
	}
	if binary != "/bin/mongodb_exporter" {
		t.Fatalf("binary = %q", binary)
	}
	wantArgs := []string{
		"--web.listen-address=127.0.0.1:19216",
		"--collector.diagnosticdata",
		"--collector.fcv",
		"--collector.dbstats",
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestParseSourceRejectsUnsupportedMongoDBExporterCollector(t *testing.T) {
	_, err := parseSource(0, map[string]interface{}{
		"id":      "mongo-prod",
		"db_type": "mongodb",
		"connection": map[string]interface{}{
			"type": "managed",
			"path": "/var/lib/ongrid-edge/secrets/mongo-prod.dsn",
		},
		"exporter": map[string]interface{}{
			"collectors": []interface{}{"collect-all"},
		},
	})
	if err == nil {
		t.Fatal("parseSource() error = nil, want unsupported collector error")
	}
	if !strings.Contains(err.Error(), `exporter.collectors unsupported "collect-all"`) {
		t.Fatalf("parseSource() error = %v", err)
	}
}

func TestParseSpecRejectsDuplicateListenPort(t *testing.T) {
	_, err := parseSpec(map[string]interface{}{
		"sources": []interface{}{
			map[string]interface{}{
				"id":             "mysql-prod",
				"db_type":        "mysql",
				"listen_address": "127.0.0.1:19104",
				"connection": map[string]interface{}{
					"type": "managed",
					"path": "/var/lib/ongrid-edge/secrets/mysql-prod.my.cnf",
				},
			},
			map[string]interface{}{
				"id":             "mysql-copy",
				"db_type":        "mysql",
				"listen_address": "0.0.0.0:19104",
				"connection": map[string]interface{}{
					"type": "managed",
					"path": "/var/lib/ongrid-edge/secrets/mysql-copy.my.cnf",
				},
			},
		},
	})
	if err == nil {
		t.Fatal("parseSpec() error = nil, want duplicate port error")
	}
	if !strings.Contains(err.Error(), "port 19104 conflicts with source") {
		t.Fatalf("parseSpec() error = %v", err)
	}
}

func TestParseSpecRejectsReservedListenPort(t *testing.T) {
	_, err := parseSpec(map[string]interface{}{
		"sources": []interface{}{
			map[string]interface{}{
				"id":             "mysql-prod",
				"db_type":        "mysql",
				"listen_address": "127.0.0.1:9102",
				"connection": map[string]interface{}{
					"type": "managed",
					"path": "/var/lib/ongrid-edge/secrets/mysql-prod.my.cnf",
				},
			},
		},
	})
	if err == nil {
		t.Fatal("parseSpec() error = nil, want reserved port error")
	}
	if !strings.Contains(err.Error(), "port 9102 conflicts with hostmetrics") {
		t.Fatalf("parseSpec() error = %v", err)
	}
}
