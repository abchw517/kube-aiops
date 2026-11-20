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
	var expected any
	if err := json.Unmarshal(document, &expected); err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`<script type="application/json" id="catalog">(.+)</script>`)
	for _, name := range []string{"prototype.html", "group-admin-prototype.html"} {
		prototype, err := os.ReadFile("../../../../docx/permission-catalog/" + name)
		if err != nil {
			t.Fatal(err)
		}
		match := pattern.FindSubmatch(prototype)
		if len(match) != 2 {
			t.Fatalf("%s: missing embedded catalog", name)
		}
		var actual any
		if err := json.Unmarshal(match[1], &actual); err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("%s: prototype and server catalog differ (%v)", name, err)
		}
	}
}
