package scenarios_test

import (
	"encoding/json"
	"os"
	"slices"
	"sort"
	"testing"

	"heartbeat/tools/patientzero/internal/app"
	"heartbeat/tools/patientzero/internal/inject"
	"heartbeat/tools/patientzero/internal/scenario"
	"heartbeat/tools/patientzero/scenarios"
)

// registry is the registry PatientZero's main builds.
func registry() *scenario.Registry {
	reg := scenario.NewRegistry()
	inject.Register(reg, &inject.Injector{}, app.Modules)
	return reg
}

// Every scenario file in the image must load: PatientZero refuses to start
// otherwise.
func TestScenarioFilesLoad(t *testing.T) {
	list, err := scenario.LoadDir(scenarios.FS, ".", registry())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Fatal("no scenario files")
	}
	// A scenario is a test: it injects something and expects something.
	for _, s := range list {
		if len(s.FaultTypes()) == 0 || len(s.Signals) == 0 {
			t.Errorf("%s: needs at least one fault and one signal", s.Name)
		}
	}
}

func readSchema(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("scenario.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("scenario.schema.json: %v", err)
	}
	return m
}

func path(t *testing.T, m map[string]any, keys ...string) any {
	t.Helper()
	var cur any = m
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("schema: %v: not an object at %q", keys, k)
		}
		cur = obj[k]
	}
	return cur
}

func strings(t *testing.T, v any) []string {
	t.Helper()
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("schema: %v is not a list", v)
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.(string))
	}
	sort.Strings(out)
	return out
}

func number(t *testing.T, v any) int {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("schema: %v is not a number", v)
	}
	return int(f)
}

// The schema is for editors; the Go validation is what PatientZero
// enforces. The lists and limits they share must agree, or the schema would
// accept a file PatientZero refuses (or the reverse).
func TestSchemaMatchesValidation(t *testing.T) {
	s := readSchema(t)
	var types []string
	for _, ft := range registry().Types() {
		types = append(types, ft.Name)
	}
	if got := strings(t, path(t, s, "$defs", "fault", "properties", "type", "enum")); !slices.Equal(got, types) {
		t.Errorf("schema fault types %v, registry %v", got, types)
	}
	entryPoints := slices.Sorted(slices.Values(app.EntryPoints))
	if got := strings(t, path(t, s, "$defs", "entryPoints", "items", "enum")); !slices.Equal(got, entryPoints) {
		t.Errorf("schema entry points %v, app.EntryPoints %v", got, entryPoints)
	}
	var modules []string
	for m := range app.Modules {
		modules = append(modules, m)
	}
	sort.Strings(modules)
	if got := strings(t, path(t, s, "$defs", "module", "enum")); !slices.Equal(got, modules) {
		t.Errorf("schema modules %v, app.Modules %v", got, modules)
	}
	logTypes := slices.Sorted(slices.Values(scenario.LogTypes))
	if got := strings(t, path(t, s, "$defs", "signal", "properties", "delivery", "properties", "log_types", "items", "enum")); !slices.Equal(got, logTypes) {
		t.Errorf("schema delivery log types %v, scenario.LogTypes %v", got, logTypes)
	}
	loads := slices.Sorted(slices.Values(scenario.LoadProfiles))
	if got := strings(t, path(t, s, "properties", "load", "enum")); !slices.Equal(got, loads) {
		t.Errorf("schema load profiles %v, scenario.LoadProfiles %v", got, loads)
	}
	if got := number(t, path(t, s, "properties", "steps", "maxItems")); got != scenario.MaxSteps {
		t.Errorf("schema steps maxItems %d, scenario.MaxSteps %d", got, scenario.MaxSteps)
	}
	if got := number(t, path(t, s, "properties", "signals", "maxItems")); got != scenario.MaxSignals {
		t.Errorf("schema signals maxItems %d, scenario.MaxSignals %d", got, scenario.MaxSignals)
	}
	// Each fault type has its parameters in the schema.
	allOf, _ := path(t, s, "$defs", "fault", "allOf").([]any)
	var described []string
	for _, a := range allOf {
		described = append(described, path(t, a.(map[string]any), "if", "properties", "type", "const").(string))
		if path(t, a.(map[string]any), "if", "properties", "type", "const") == "log-flood" {
			params := path(t, a.(map[string]any), "then", "properties", "params", "properties").(map[string]any)
			if rows := number(t, path(t, params, "rows", "maximum")); rows != inject.MaxFloodRows {
				t.Errorf("schema log-flood rows maximum %d, inject.MaxFloodRows %d", rows, inject.MaxFloodRows)
			}
			if bytes := number(t, path(t, params, "bytes", "maximum")); bytes != inject.MaxFloodBytes {
				t.Errorf("schema log-flood bytes maximum %d, inject.MaxFloodBytes %d", bytes, inject.MaxFloodBytes)
			}
		}
		for typ, field := range map[string]string{"errors": "message", "deploy": "exception"} {
			if path(t, a.(map[string]any), "if", "properties", "type", "const") != typ {
				continue
			}
			max := number(t, path(t, a.(map[string]any), "then", "properties", "params", "properties", field, "maxLength"))
			if max != inject.MaxMessageLen {
				t.Errorf("schema %s %s maxLength %d, inject.MaxMessageLen %d", typ, field, max, inject.MaxMessageLen)
			}
		}
	}
	sort.Strings(described)
	if !slices.Equal(described, types) {
		t.Errorf("schema describes the params of %v, registry has %v", described, types)
	}
}
