package api

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"server-monitor/internal/alerts"
	"server-monitor/internal/collector"
	"server-monitor/internal/notify"
	"server-monitor/internal/parser"
)

// Test spójności kontraktu: typy w web/src/types/api.ts muszą odpowiadać 1:1 strukturom JSON
// zwracanym przez API (nazwy pól, opcjonalność, podstawowe typy). Wymóg z CLAUDE.md §3.

type tsField struct {
	optional bool
	typ      string
}

func readTS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../web/src/types/api.ts")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func parseTS(t *testing.T) map[string]map[string]tsField {
	t.Helper()
	typeRe := regexp.MustCompile(`(?s)export type (\w+) = \{(.*?)\n\};`)
	fieldRe := regexp.MustCompile(`(?m)^  (\w+)(\??): ([^;]+);`)
	out := map[string]map[string]tsField{}
	for _, m := range typeRe.FindAllStringSubmatch(readTS(t), -1) {
		fields := map[string]tsField{}
		for _, f := range fieldRe.FindAllStringSubmatch(m[2], -1) {
			fields[f[1]] = tsField{optional: f[2] == "?", typ: strings.TrimSpace(f[3])}
		}
		out[m[1]] = fields
	}
	return out
}

func primitive(k reflect.Kind) string {
	switch k {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return "number"
	}
	return ""
}

func TestTypeScriptContractMatchesGoDTOs(t *testing.T) {
	ts := parseTS(t)
	pairs := []struct {
		goType reflect.Type
		tsName string
	}{
		{reflect.TypeOf(HealthResponse{}), "HealthResponse"},
		{reflect.TypeOf(Clusters{}), "Clusters"},
		{reflect.TypeOf(collector.SystemdUnit{}), "SystemdUnitHealth"},
		{reflect.TypeOf(parser.SupervisorGroup{}), "SupervisorGroup"},
		{reflect.TypeOf(parser.SupervisorWorker{}), "SupervisorWorker"},
		{reflect.TypeOf(collector.ElasticsearchPayload{}), "ElasticsearchHealth"},
		{reflect.TypeOf(collector.RedisPayload{}), "RedisHealth"},
		{reflect.TypeOf(collector.KibanaPayload{}), "KibanaHealth"},
		{reflect.TypeOf(UnitDTO{}), "UnitInfo"},
		{reflect.TypeOf(UnitsResponse{}), "UnitsResponse"},
		{reflect.TypeOf(SelectionRequest{}), "SelectionRequest"},
		{reflect.TypeOf(SessionDTO{}), "SessionInfo"},
		{reflect.TypeOf(SessionsResponse{}), "SessionsResponse"},
		{reflect.TypeOf(CommandDTO{}), "CommandInfo"},
		{reflect.TypeOf(CommandsResponse{}), "CommandsResponse"},
		{reflect.TypeOf(LoginFailureDTO{}), "LoginFailureInfo"},
		{reflect.TypeOf(LoginFailuresResponse{}), "LoginFailuresResponse"},
		{reflect.TypeOf(alerts.Settings{}), "AlertSettings"},
		{reflect.TypeOf(alerts.LoginTimeRule{}), "LoginTimeRule"},
		{reflect.TypeOf(alerts.Window{}), "AlertWindow"},
		{reflect.TypeOf(alerts.LoginIPRule{}), "LoginIPRule"},
		{reflect.TypeOf(alerts.ServiceStateRule{}), "ServiceStateRule"},
		{reflect.TypeOf(notify.EmailConfig{}), "EmailConfig"},
		{reflect.TypeOf(ConnectorDTO{}), "ConnectorInfo"},
		{reflect.TypeOf(ConnectorsResponse{}), "ConnectorsResponse"},
		{reflect.TypeOf(ConnectorUpdateRequest{}), "ConnectorUpdateRequest"},
		{reflect.TypeOf(TestConnectorResponse{}), "TestConnectorResponse"},
		{reflect.TypeOf(NotificationDTO{}), "NotificationInfo"},
		{reflect.TypeOf(NotificationsResponse{}), "NotificationsResponse"},
		{reflect.TypeOf(ErrorResponse{}), "ErrorResponse"},
	}
	covered := map[string]bool{}
	for _, p := range pairs {
		covered[p.tsName] = true
		fields, ok := ts[p.tsName]
		if !ok {
			t.Errorf("api.ts has no type %s (for Go %s)", p.tsName, p.goType)
			continue
		}
		seen := map[string]bool{}
		for i := 0; i < p.goType.NumField(); i++ {
			sf := p.goType.Field(i)
			name, opts, _ := strings.Cut(sf.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				t.Errorf("%s.%s has no json tag", p.goType, sf.Name)
				continue
			}
			seen[name] = true
			tf, ok := fields[name]
			if !ok {
				t.Errorf("%s: Go field %q missing in TS type %s", p.goType, name, p.tsName)
				continue
			}
			omit := strings.Contains(opts, "omitempty")
			if tf.optional != omit {
				t.Errorf("%s.%s: TS optional=%v but Go omitempty=%v", p.tsName, name, tf.optional, omit)
			}
			ft := sf.Type
			ptr := ft.Kind() == reflect.Pointer
			if ptr {
				ft = ft.Elem()
			}
			switch {
			case ft.Kind() == reflect.Slice:
				if !strings.HasSuffix(tf.typ, "[]") {
					t.Errorf("%s.%s: Go slice, TS %q", p.tsName, name, tf.typ)
				}
			case primitive(ft.Kind()) != "":
				want := primitive(ft.Kind())
				if ptr && !omit {
					want += " | null"
				}
				// pola "status" mają w TS węższy typ wyliczeniowy (wartości sprawdza TestTypeScriptStatusEnums)
				if (name == "status" || name == "overall_status") && want == "string" && (tf.typ == "Status" || tf.typ == "GroupStatus") {
					continue
				}
				if tf.typ != want {
					t.Errorf("%s.%s: TS type %q, want %q", p.tsName, name, tf.typ, want)
				}
			}
		}
		for name := range fields {
			if !seen[name] {
				t.Errorf("TS type %s has field %q that Go %s does not return", p.tsName, name, p.goType)
			}
		}
	}
	for name := range ts {
		if !covered[name] {
			t.Errorf("api.ts type %s is not covered by the contract test", name)
		}
	}
}

// Enumy statusów w TS muszą odpowiadać wartościom używanym po stronie Go.
func TestTypeScriptStatusEnums(t *testing.T) {
	src := readTS(t)
	for _, want := range []string{
		`export type Status = 'OK' | 'WARN' | 'CRIT';`,
		`export type GroupStatus = 'OK' | 'DEGRADED' | 'DOWN';`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("api.ts missing %q", want)
		}
	}
	for _, s := range []string{"OK", "WARN", "CRIT"} {
		if collector.Worst(s) != s {
			t.Errorf("Go does not treat %q as a valid status", s)
		}
	}
	g := parser.GroupSupervisor([]parser.SupervisorProcess{
		{Name: "a_00", Group: "a", StateName: "RUNNING"}, {Name: "a_01", Group: "a", StateName: "FATAL"},
		{Name: "b_00", Group: "b", StateName: "FATAL"}, {Name: "c_00", Group: "c", StateName: "RUNNING"}})
	got := map[string]string{}
	for _, x := range g {
		got[x.Group] = x.Status
	}
	if got["a"] != "DEGRADED" || got["b"] != "DOWN" || got["c"] != "OK" {
		t.Errorf("group statuses %v", got)
	}
}
