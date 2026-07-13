package main

import (
	"encoding/json"
	"flag"
	"fmt"
)

func cmdProjects(args []string) error {
	return cmdResourceList(args, "projects", "project", "/api/v1/projects")
}

func cmdGroups(args []string) error {
	return cmdResourceList(args, "groups", "group", "/api/v1/groups")
}

// cmdResourceList implements the shared shape of `bb projects` / `bb groups`:
// list all, or create one with --create [--slug].
func cmdResourceList(args []string, fsName, singular, apiPath string) error {
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
		data, err := client.post(apiPath, body)
		if err != nil {
			return err
		}
		return writeRaw(data)
	}

	data, err := client.get(apiPath)
	if err != nil {
		return err
	}
	return writeRaw(data)
}

func cmdAPIKeys(args []string) error {
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
