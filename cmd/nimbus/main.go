package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	tea "charm.land/bubbletea/v2"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/deploy"
	"github.com/wagnerbm/nimbusv2/internal/resources"
	"github.com/wagnerbm/nimbusv2/internal/trace"
	"github.com/wagnerbm/nimbusv2/internal/ui"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "application" || os.Args[1] == "applications") {
		handleApplication(os.Args[2:])
		return
	}
	handleTUI(os.Args[1:])
}

func handleTUI(args []string) {
	fs := flag.NewFlagSet("nimbus", flag.ExitOnError)
	traceEnabled := fs.Bool("trace", false, "Enable trace logging of TUI actions")
	traceFile := fs.String("trace-file", "nimbus-trace.log", "Path to trace output file")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: nimbus [flags]\n       nimbus application(s) <command> [flags]\n\nCommands:\n  application list     List applications\n  application deploy   Build and deploy to target instance\n\nFlags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	var tracePath string
	if *traceEnabled {
		tracePath = *traceFile
	}

	logger, err := trace.New(tracePath)
	if err != nil {
		fatal(err)
	}
	defer logger.Close()

	logger.Log("nimbus starting")
	model := ui.NewModel(logger)
	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		fatal(err)
	}
	logger.Log("nimbus exiting")
}

func handleApplication(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: nimbus application <command> [flags]\n\nCommands:\n  list     List applications\n  deploy   Build and deploy to target instance\n")
		os.Exit(1)
	}

	switch args[0] {
	case "list", "ls":
		handleList(args[1:])
	case "deploy":
		handleDeploy(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: nimbus application %s\n", args[0])
		os.Exit(1)
	}
}

func handleList(args []string) {
	fs := flag.NewFlagSet("nimbus application list", flag.ExitOnError)
	region := fs.String("region", "", "AWS region (defaults to AWS_REGION or us-east-1)")
	fs.Parse(args)

	r := resolveRegion(*region)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := aws.NewClient(ctx, r)
	if err != nil {
		fatal(err)
	}

	provider := resources.NewLightsailApplicationProvider(client)
	apps, err := provider.Fetch(ctx, r)
	if err != nil {
		fatal(err)
	}

	if len(apps) == 0 {
		fmt.Println("No applications found")
		return
	}

	fmt.Printf("%-20s %-10s %-40s %s\n", "NAME", "STATE", "BUCKET", "REGION")
	for _, a := range apps {
		vals := a.Values()
		fmt.Printf("%-20s %-10s %-40s %s\n", vals[0], vals[1], vals[2], vals[3])
	}
}

func handleDeploy(args []string) {
	fs := flag.NewFlagSet("nimbus application deploy", flag.ExitOnError)
	name := fs.String("name", "", "Application name (required)")
	env := fs.String("env", "dev", "Environment name")
	region := fs.String("region", "", "AWS region (defaults to AWS_REGION or us-east-1)")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		fs.Usage()
		os.Exit(1)
	}

	r := resolveRegion(*region)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := aws.NewClient(ctx, r)
	if err != nil {
		fatal(err)
	}

	if err := deploy.Deploy(ctx, client, *name, *env, r); err != nil {
		fatal(err)
	}
}

func resolveRegion(r string) string {
	if r != "" {
		return r
	}
	if v := os.Getenv("AWS_REGION"); v != "" {
		return v
	}
	if v := os.Getenv("AWS_DEFAULT_REGION"); v != "" {
		return v
	}
	return "us-east-1"
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
