package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Scope is where a fault type acts, and so which runner can run it.
type Scope string

const (
	// ScopeInProcess faults change how PatientZero itself answers (latency,
	// errors on its entry points). PatientZero's own runner runs them; they
	// need no access to the cluster.
	ScopeInProcess Scope = "in-process"
	// ScopeCluster faults act on other workloads: restarting or stopping the
	// db-collector, the OTel Collector or Loki, stopping SQL Server. They
	// need Kubernetes RBAC or the Docker host, which PatientZero does not
	// have, so its runner refuses them; a runner with that access runs them
	// (scenarios/README.md, "Cluster faults").
	ScopeCluster Scope = "cluster"
)

// Fault is one fault, ready to apply. Start applies it and Stop removes it.
// The runner calls Stop exactly once after a successful Start, also when the
// run is stopped, and always with a fresh context.
type Fault interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// FaultType is one entry of the registry.
type FaultType struct {
	// Name is the type scenario files use, such as latency.
	Name string
	// Scope says which runner can run it.
	Scope Scope
	// Description is one line for listings.
	Description string
	// New decodes and validates the parameters and returns the fault, not
	// yet started. Typed parameters are decoded with DecodeParams.
	New func(params map[string]any) (Fault, error)
}

// Registry is the set of fault types a runner knows. Validation needs every
// type a scenario may use; a runner refuses to start the types whose scope
// it does not run.
type Registry struct {
	types map[string]FaultType
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{types: map[string]FaultType{}} }

// Register adds a fault type. A second type with the same name, an invalid
// name or scope, or a missing constructor is a programming error and panics.
func (r *Registry) Register(t FaultType) {
	if !ValidName(t.Name) || t.New == nil || (t.Scope != ScopeInProcess && t.Scope != ScopeCluster) {
		panic(fmt.Sprintf("scenario: invalid fault type %q", t.Name))
	}
	if _, ok := r.types[t.Name]; ok {
		panic(fmt.Sprintf("scenario: fault type %q registered twice", t.Name))
	}
	r.types[t.Name] = t
}

// Type returns the fault type called name.
func (r *Registry) Type(name string) (FaultType, bool) {
	t, ok := r.types[name]
	return t, ok
}

// Types lists the registered fault types by name.
func (r *Registry) Types() []FaultType {
	out := make([]FaultType, 0, len(r.types))
	for _, t := range r.types {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// New builds the fault f names, checking its parameters. It does not start
// it.
func (r *Registry) New(f FaultSpec) (Fault, error) {
	t, ok := r.types[f.Type]
	if !ok {
		names := make([]string, 0, len(r.types))
		for _, t := range r.Types() {
			names = append(names, t.Name)
		}
		return nil, fmt.Errorf("type %q is not a registered fault type (registered: %s)", f.Type, strings.Join(names, ", "))
	}
	fault, err := t.New(f.Params)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Type, err)
	}
	return fault, nil
}

// DecodeParams decodes a fault's parameters into v, a pointer to a struct
// with json tags, rejecting unknown parameters.
func DecodeParams(params map[string]any, v any) error {
	if params == nil {
		params = map[string]any{}
	}
	b, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("params: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("params: %w", err)
	}
	return nil
}
