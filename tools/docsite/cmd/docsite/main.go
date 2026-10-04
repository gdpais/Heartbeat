// Command docsite renders the Markdown documentation into the static HTML
// site under docs/site, or with -check reports whether that site is stale.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"heartbeat/tools/docsite/internal/site"
)

func main() {
	cfg := site.Config{}
	flag.StringVar(&cfg.Root, "root", ".", "repository root")
	flag.StringVar(&cfg.OutDir, "out", "docs/site", "output directory, relative to -root")
	flag.StringVar(&cfg.RepoURL, "repo-url", "", "repository web URL used for links to source files (required)")
	flag.StringVar(&cfg.Ref, "ref", "master", "branch or tag used for links to source files")
	check := flag.Bool("check", false, "fail if the output directory differs from a fresh build instead of writing it")
	flag.Parse()

	files, err := site.Build(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if !*check {
		if err := site.Write(cfg, files); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("docsite: wrote %d files to %s\n", len(files), cfg.OutDir)
		return
	}
	diffs, err := site.Check(cfg, files)
	if err != nil {
		log.Fatal(err)
	}
	if len(diffs) > 0 {
		for _, d := range diffs {
			fmt.Fprintln(os.Stderr, "docsite:", d)
		}
		fmt.Fprintf(os.Stderr, "docsite: %s is out of date with the Markdown; run make docs-site and commit the result\n", cfg.OutDir)
		os.Exit(1)
	}
	fmt.Printf("docsite: %s is up to date\n", cfg.OutDir)
}
