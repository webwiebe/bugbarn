package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"strings"
)

func cmdProjects(args []string) error {
	if wantsMerge(args) {
		return cmdProjectMerge(args)
	}
	// Ask for usage counts explicitly: the API omits them by default because
	// computing them scans the events and log_entries tables in full, but they
	// are part of what `bb projects` has always reported and this is a
	// low-frequency interactive command.
	return cmdResourceList(args, "projects", "project", "/api/v1/projects", "/api/v1/projects?usage=true")
}

// wantsMerge reports whether the arguments ask for a merge or the merge record.
func wantsMerge(args []string) bool {
	for _, a := range args {
		name := strings.TrimLeft(a, "-")
		if name == "merges" || name == "merge" || strings.HasPrefix(name, "merge=") {
			return true
		}
	}
	return false
}

// cmdProjectMerge implements `bb projects --merge SRC --into TARGET [--env ENV]`
// and `bb projects --merges`. The merge moves SRC's data into TARGET stamped
// with ENV, archives SRC and keeps its ingest keys and slug routing to TARGET.
func cmdProjectMerge(args []string) error {
	fs := flag.NewFlagSet("projects", flag.ContinueOnError)
	source := fs.String("merge", "", "slug of the project to merge away")
	target := fs.String("into", "", "slug of the project that receives the data")
	env := fs.String("env", "", "environment the source stands for (default: the source slug suffix, else production)")
	list := fs.Bool("merges", false, "list the recorded merges with the rows moved")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if *list {
		data, err := client.get("/api/v1/project-merges")
		if err != nil {
			return err
		}
		return writeRaw(data)
	}
	if *source == "" || *target == "" {
		return fmt.Errorf("usage: bb projects --merge SOURCE --into TARGET [--env ENV]")
	}
	body := map[string]string{"target": *target}
	if *env != "" {
		body["environment"] = *env
	}
	data, err := client.post("/api/v1/projects/"+url.PathEscape(*source)+"/merge", body)
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func cmdGroups(args []string) error {
	return cmdResourceList(args, "groups", "group", "/api/v1/groups", "/api/v1/groups")
}

// cmdResourceList implements the shared shape of `bb projects` / `bb groups`:
// list all, or create one with --create [--slug]. createPath is the collection
// endpoint; listPath may carry query parameters that only apply to reads.
func cmdResourceList(args []string, fsName, singular, createPath, listPath string) error {
	fs := flag.NewFlagSet(fsName, flag.ContinueOnError)
	create := fs.String("create", "", "create a new "+singular+" with this name")
	slug := fs.String("slug", "", singular+" slug (defaults to slugified name)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := newClient()
	if err != nil {
		return err
	}

	if *create != "" {
		body := map[string]string{"name": *create}
		if *slug != "" {
			body["slug"] = *slug
		}
		data, err := client.post(createPath, body)
		if err != nil {
			return err
		}
		return writeRaw(data)
	}

	data, err := client.get(listPath)
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func cmdAPIKeys(args []string) error {
	fs := flag.NewFlagSet("apikeys", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: bb apikeys")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	data, err := client.get("/api/v1/apikeys")
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func validateProject(c *Client, slug string) error {
	data, err := c.get("/api/v1/projects")
	if err != nil {
		return err
	}
	var resp struct {
		Projects []struct {
			Slug string `json:"slug"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse projects response: %w", err)
	}
	for _, p := range resp.Projects {
		if p.Slug == slug {
			return nil
		}
	}
	return fmt.Errorf("project %q not found — run 'bb projects' to see available projects", slug)
}

func validateGroup(c *Client, slug string) error {
	data, err := c.get("/api/v1/groups")
	if err != nil {
		return err
	}
	var resp struct {
		Groups []struct {
			Slug string `json:"slug"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse groups response: %w", err)
	}
	for _, g := range resp.Groups {
		if g.Slug == slug {
			return nil
		}
	}
	return fmt.Errorf("group %q not found — run 'bb groups' to see available groups", slug)
}
