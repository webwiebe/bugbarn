package main

import "fmt"

// scopeClient scopes the client to a group or a project, for every command that
// takes both --project and --group (issues, logs, tui). Resolution order:
// explicit flag, then .bugbarn.json (group, then project), then the global
// config's project. A scope that names a project or group the server does not
// have is an error.
func scopeClient(client *Client, project, group string) error {
	if group != "" && project != "" {
		return fmt.Errorf("--group and --project are mutually exclusive")
	}
	resolvedGroup := resolveGroup(group)
	if resolvedGroup != "" && project == "" {
		if err := validateGroup(client, resolvedGroup); err != nil {
			return err
		}
		client.group = resolvedGroup
		return nil
	}
	projectSlug := resolveProject(project, client)
	if projectSlug == "" {
		return nil
	}
	if err := validateProject(client, projectSlug); err != nil {
		return err
	}
	client.project = projectSlug
	return nil
}

// newProjectClient builds a client scoped to the project named by the flag, the
// directory's .bugbarn.json or the global config, in that order. It serves the
// commands that take --project only.
func newProjectClient(flagValue string) (*Client, error) {
	client, err := newClient()
	if err != nil {
		return nil, err
	}
	if p := resolveProject(flagValue, client); p != "" {
		client.project = p
	}
	return client, nil
}

func resolveProject(flag string, c *Client) string {
	if flag != "" {
		return flag
	}
	if lc, ok := findLocalConfig(); ok && lc.Project != "" {
		return lc.Project
	}
	return c.config.Project
}

func resolveGroup(flag string) string {
	if flag != "" {
		return flag
	}
	if lc, ok := findLocalConfig(); ok {
		return lc.Group
	}
	return ""
}
