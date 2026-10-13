package main

import "flag"

// cmdMonitors lists the check-in monitors of a project (JSON output), with
// monitors that need attention (error, missed) first.
func cmdMonitors(args []string) error {
	fs := flag.NewFlagSet("monitors", flag.ContinueOnError)
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
	data, err := client.get("/api/v1/monitors")
	if err != nil {
		return err
	}
	return writeRaw(data)
}
