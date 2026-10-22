// Package scenarios holds PatientZero's scenario files, built into the
// binary so the image always carries the scenarios of its own commit. The
// format is in README.md and scenario.schema.json.
package scenarios

import "embed"

// FS holds the scenario files, *.yaml at its root.
//
//go:embed *.yaml
var FS embed.FS
