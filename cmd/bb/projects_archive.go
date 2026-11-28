package main

import (
	"encoding/json"
	"errors"
	"flag"
	"net/url"
	"strings"
)

// hasFlag reports whether args carry the named flag, with one or two dashes and
// with or without an =value.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		for _, dashes := range []string{"-", "--"} {
			if a == dashes+name || strings.HasPrefix(a, dashes+name+"=") {
				return true
			}
		}
	}
	return false
}

// cmdProjectsArchive implements `bb projects --archive SLUG` and
// `bb projects --unarchive SLUG`. Archive refuses a project that received data
// in the last 7 days and prints the time of the project's last event.
func cmdProjectsArchive(args []string) error {
	fs := flag.NewFlagSet("projects", flag.ContinueOnError)
	archive := fs.String("archive", "", "slug of the project to archive")
	unarchive := fs.String("unarchive", "", "slug of the archived project to bring back")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*archive == "") == (*unarchive == "") {
		return errors.New("usage: bb projects --archive SLUG | --unarchive SLUG")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	action, slug := "archive", *archive
	if *unarchive != "" {
		action, slug = "unarchive", *unarchive
	}
	data, err := client.post("/api/v1/projects/"+url.PathEscape(slug)+"/"+action, map[string]string{})
	if err != nil {
		return err
	}
	return writeRaw(data)
}

// cmdProjectsArchived implements `bb projects --archived`: the projects the
// default list hides, each with its usage counts, so an archive can be checked
// and a mistaken one brought back with --unarchive.
func cmdProjectsArchived(args []string) error {
	fs := flag.NewFlagSet("projects", flag.ContinueOnError)
	fs.Bool("archived", false, "list the archived projects")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	data, err := client.get("/api/v1/projects?usage=true&include_archived=true")
	if err != nil {
		return err
	}
	var resp struct {
		Projects []json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return err
	}
	archived := make([]json.RawMessage, 0, len(resp.Projects))
	for _, raw := range resp.Projects {
		var p struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if p.Status == "archived" {
			archived = append(archived, raw)
		}
	}
	out, err := json.Marshal(map[string]any{"projects": archived})
	if err != nil {
		return err
	}
	return writeRaw(out)
}
