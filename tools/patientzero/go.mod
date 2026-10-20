// PatientZero is its own module, like tools/docsite: test code stays out of
// the product's module, images and releases (ADR 0010).
module heartbeat/tools/patientzero

go 1.27.2

require (
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9
	github.com/microsoft/go-mssqldb v1.8.2
)

require (
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/crypto v0.24.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
)
