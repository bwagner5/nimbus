package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

// promptSelect shows a numbered list and returns the selected index.
func promptSelect(label string, items []string) int {
	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "No options available")
		os.Exit(1)
	}
	if len(items) == 1 {
		fmt.Fprintf(os.Stderr, "%s: %s\n", label, items[0])
		return 0
	}
	fmt.Fprintf(os.Stderr, "%s:\n", label)
	for i, item := range items {
		fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, item)
	}
	for {
		fmt.Fprintf(os.Stderr, "Choice [1-%d]: ", len(items))
		var choice int
		if _, err := fmt.Scan(&choice); err != nil || choice < 1 || choice > len(items) {
			continue
		}
		return choice - 1
	}
}

// selectApp prompts the user to select an app if name is empty. Returns app name and the App.
func selectApp(ctx context.Context, client *aws.Client, name, region string) (applications.App, error) {
	appClient := applications.NewClient(client)
	apps, err := appClient.List(ctx, region)
	if err != nil {
		return applications.App{}, err
	}
	if len(apps) == 0 {
		return applications.App{}, fmt.Errorf("no applications found")
	}
	if name != "" {
		for _, a := range apps {
			if a.Name == name {
				return a, nil
			}
		}
		return applications.App{}, fmt.Errorf("application %q not found", name)
	}
	names := make([]string, len(apps))
	for i, a := range apps {
		names[i] = a.Name
	}
	idx := promptSelect("Select application", names)
	return apps[idx], nil
}

// selectEnv prompts the user to select an env if env is empty.
func selectEnv(app applications.App, env string) string {
	if env != "" {
		return env
	}
	if len(app.Envs) == 1 {
		fmt.Fprintf(os.Stderr, "Environment: %s\n", app.Envs[0])
		return app.Envs[0]
	}
	idx := promptSelect("Select environment", app.Envs)
	return app.Envs[idx]
}

// selectTarget prompts the user to select a target instance if instance is empty.
func selectTarget(ctx context.Context, client *aws.Client, appName, envName, region string, instance string) (string, error) {
	if instance != "" {
		return instance, nil
	}
	appClient := applications.NewClient(client)
	targets, err := appClient.ListInstances(ctx, region)
	if err != nil {
		return "", err
	}
	// Filter to instances tagged for this app/env
	var matched []applications.Target
	detail, err := appClient.GetDetail(ctx, appName, "", region)
	if err == nil {
		for _, env := range detail.Environments {
			if env.Name == envName {
				matched = env.Targets
				break
			}
		}
	}
	if len(matched) == 0 {
		// Fall back to all instances
		matched = targets
	}
	if len(matched) == 0 {
		return "", fmt.Errorf("no instances found")
	}
	names := make([]string, len(matched))
	for i, t := range matched {
		names[i] = fmt.Sprintf("%-24s %-10s %s", t.Name, t.State, t.IP)
	}
	idx := promptSelect("Select instance", names)
	return matched[idx].Name, nil
}
