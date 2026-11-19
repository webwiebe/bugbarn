package main

import (
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
