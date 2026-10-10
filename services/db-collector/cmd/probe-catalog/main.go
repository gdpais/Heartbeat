// Package main prints the built-in SQL Server probe catalog, so a developer
// can see and run exactly what the collector sends to a target.
//
// Without arguments it lists every probe with its metrics: name, type, source
// column, unit scale and label columns.  With -sql it prints one probe's batch
// as the collector sends it, session settings included, ready to pipe into
// sqlcmd:
//
//	go run ./services/db-collector/cmd/probe-catalog
//	go run ./services/db-collector/cmd/probe-catalog -sql waits
//
// It is a development aid; the db-collector image does not include it.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	connector "heartbeat/services/db-collector/internal/connectors/sqlserver"
	catalogsqlserver "heartbeat/services/db-collector/internal/probes/sqlserver"
)

func main() {
	probe := flag.String("sql", "", "print the batch the collector sends for this probe")
	flag.Parse()
	if err := run(os.Stdout, catalogsqlserver.DefaultCatalog(), *probe); err != nil {
		fmt.Fprintln(os.Stderr, "probe-catalog:", err)
		os.Exit(2)
	}
}

func run(w io.Writer, catalog catalogsqlserver.Catalog, probe string) error {
	if probe == "" {
		return list(w, catalog)
	}
	definition, ok := catalog.Get(probe)
	if !ok {
		return fmt.Errorf("unknown probe %q; probes: %s", probe, strings.Join(catalog.Names(), ", "))
	}
	_, err := fmt.Fprintln(w, connector.WithSessionSettings(definition.QueryTemplate))
	return err
}

// list writes one line per metric, grouped by probe in catalog order.
func list(w io.Writer, catalog catalogsqlserver.Catalog) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROBE\tMETRIC\tTYPE\tCOLUMN\tSCALE\tLABELS")
	for _, name := range catalog.Names() {
		definition, _ := catalog.Get(name)
		for _, metric := range definition.Metrics {
			labels := "-"
			if len(metric.LabelColumns) > 0 {
				labels = strings.Join(metric.LabelColumns, ",")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%g\t%s\n",
				name, metric.Name, metric.Type, metric.ValueColumn, metric.Convert(1), labels)
		}
	}
	return tw.Flush()
}
