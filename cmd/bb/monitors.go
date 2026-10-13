package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
)

// cmdMonitors lists the check-in monitors of a project (JSON output), with
// monitors that need attention (error, missed) first. With --delete <slug> it
// removes one; the monitor's issues stay.
func cmdMonitors(args []string) error {
	fs := flag.NewFlagSet("monitors", flag.ContinueOnError)
	project := fs.String("project", "", "project slug (default: the current directory's project)")
	del := fs.String("delete", "", "delete the monitor with this slug (its issues stay)")
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
	if *del != "" {
		if client.project == "" && client.config.Project == "" {
			return errors.New("deleting a monitor needs a project: pass --project <slug>")
		}
		if _, err := client.do("DELETE", "/api/v1/monitors/"+url.PathEscape(*del), nil); err != nil {
			return err
		}
		fmt.Printf("deleted monitor %s\n", *del)
		return nil
	}
	data, err := client.get("/api/v1/monitors")
	if err != nil {
		return err
	}
	return writeRaw(data)
}
