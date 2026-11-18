package main

import (
	"flag"
)

// cmdEnvironments lists the environments of a project (JSON output): the issue
// count, the open count and the last activity of each, newest first.
func cmdEnvironments(args []string) error {
	fs := flag.NewFlagSet("environments", flag.ContinueOnError)
	project := fs.String("project", "", "project slug (default: the current directory's project)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if *project != "" {
		client = client.withProject(*project)
	}
	data, err := client.get("/api/v1/environments")
	if err != nil {
		return err
	}
	return writeRaw(data)
}
