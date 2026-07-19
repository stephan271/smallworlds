package launcher_test

import (
	"encoding/json"
	"os"
	"testing"
)

func TestOpenAPIContractDescribesLauncherWorkflow(t *testing.T) {
	contractBytes, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		OpenAPI string                            `json:"openapi"`
		Paths   map[string]map[string]interface{} `json:"paths"`
	}
	if err := json.Unmarshal(contractBytes, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q, want 3.1.0", contract.OpenAPI)
	}
	wanted := map[string][]string{
		"/api/v1/session":                          {"get"},
		"/api/v1/session/exchange":                 {"post"},
		"/api/v1/vault":                            {"get"},
		"/api/v1/vault/unlock":                     {"post"},
		"/api/v1/recovery-bundles/export":          {"post"},
		"/api/v1/recovery-bundles/preview":         {"post"},
		"/api/v1/recovery-bundles/import":          {"post"},
		"/api/v1/profiles":                         {"get", "post"},
		"/api/v1/profiles/{id}":                    {"put"},
		"/api/v1/profiles/{id}/journey":            {"get"},
		"/api/v1/profiles/{id}/credentials":        {"get"},
		"/api/v1/profiles/{id}/credentials/{kind}": {"put", "delete"},
		"/api/v1/plans":                            {"post"},
		"/api/v1/plans/{id}/approve":               {"post"},
		"/api/v1/runs/{id}":                        {"get"},
		"/api/v1/runs/{id}/cancel":                 {"post"},
		"/api/v1/events":                           {"get"},
	}
	for path, methods := range wanted {
		operations, ok := contract.Paths[path]
		if !ok {
			t.Errorf("OpenAPI contract lacks path %s", path)
			continue
		}
		for _, method := range methods {
			if _, ok := operations[method]; !ok {
				t.Errorf("OpenAPI contract lacks %s %s", method, path)
			}
		}
	}
}
