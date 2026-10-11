package outsystems

import (
	"os"
	"path/filepath"
	"testing"

	catalogoutsystems "heartbeat/services/db-collector/internal/probes/outsystems"
)

// fixtureDir holds one SQL Server fixture per log type: the CREATE TABLE
// statements of the oslog tables its query reads.
const fixtureDir = "testdata/oslog"

// Every log type's query runs against SQL Server in CI (make
// test-sqlserver), so every log type needs its fixture tables.  This check
// runs without SQL Server, so a missing fixture fails make test too.
func TestEveryLogTypeHasASQLServerFixture(t *testing.T) {
	for _, logType := range catalogoutsystems.DefaultCatalog().Names() {
		path := filepath.Join(fixtureDir, logType+".sql")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("log type %q has no SQL Server fixture: add services/db-collector/internal/collectors/outsystems/%s "+
				"with the CREATE TABLE statements of the oslog tables queries/%s.sql reads (%v)", logType, path, logType, err)
		}
	}
}
