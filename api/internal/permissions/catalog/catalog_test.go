package catalog

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestCatalogModulesAndPrototypeUseSameOperations(t *testing.T) {
	var data struct {
		Modules map[string]string
		Rows    []struct {
			Module string
			Points []Point
		}
	}
	if err := json.Unmarshal(document, &data); err != nil {
		t.Fatal(err)
	}
	for id, expected := range map[string]string{"diagnosis": "诊断控制台", "knowledge": "运维知识库", "resources": "资源管理", "system": "系统管理"} {
		if data.Modules[id] != expected {
			t.Fatalf("missing module %s", id)
		}
	}
	if data.Modules["platform"] != "" {
		t.Fatal("combined platform module remains registered")
	}
	for _, row := range data.Rows {
		for _, p := range row.Points {
			if data.Modules[row.Module] == "" || !strings.HasPrefix(p.Code, row.Module+".") ||
				!slices.Contains([]string{"C", "D", "U", "R"}, p.Action) || !strings.HasPrefix(p.Path, "/api/v1/") {
				t.Fatalf("invalid permission: %+v", p)
			}
		}
	}
	prototype, err := os.ReadFile("../../../../docx/permission-catalog/prototype.html")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`<script type="application/json" id="catalog">(.+)</script>`).FindSubmatch(prototype)
	if len(match) != 2 {
		t.Fatal("prototype missing embedded catalog")
	}
	var actual, expected any
	if json.Unmarshal(match[1], &actual) != nil || json.Unmarshal(document, &expected) != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatal("prototype and server catalog differ")
	}
}
