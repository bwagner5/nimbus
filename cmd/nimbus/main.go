package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/deploy"
	"github.com/wagnerbm/nimbusv2/internal/resources"
	"github.com/wagnerbm/nimbusv2/internal/trace"
	"github.com/wagnerbm/nimbusv2/internal/ui"
	"github.com/wagnerbm/nimbusv2/internal/watch"
)

var appCommands = map[string]bool{
	"app": true, "application": true, "applications": true,
}

var knownCommands = []string{"app", "application", "applications"}

func main() {
	if len(os.Args) < 2 {
		handleTUI(os.Args[1:])
		return
	}
	cmd := os.Args[1]
	if appCommands[cmd] {
		handleApplication(os.Args[2:])
		return
	}
	if strings.HasPrefix(cmd, "-") {
		handleTUI(os.Args[1:])
		return
	}
	fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
	if s := suggest(cmd, knownCommands); s != "" {
		fmt.Fprintf(os.Stderr, "Did you mean: nimbus %s?\n", s)
	}
	fmt.Fprintf(os.Stderr, "\nRun 'nimbus --help' for usage.\n")
	os.Exit(1)
}

func suggest(input string, candidates []string) string {
	best := ""
	bestDist := 3
	for _, c := range candidates {
		d := levenshtein(input, c)
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr := make([]int, lb+1)
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev = curr
	}
	return prev[lb]
}

func handleTUI(args []string) {
	fs := flag.NewFlagSet("nimbus", flag.ExitOnError)
	traceEnabled := fs.Bool("trace", false, "Enable trace logging of TUI actions")
	traceFile := fs.String("trace-file", "nimbus-trace.log", "Path to trace output file")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: nimbus [flags]
       nimbus application(s) <command> [flags]

Commands:
  application list            List applications
  application deploy          Deploy to target (bucket-based, or --ssh)
  application delete          Delete an application
  application watch           Watch for new deployments (runs on instance)
  application install-watch   Install systemd watch service on instance
  application disassociate    Remove an instance as a deployment target

Flags:
`)
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
		fmt.Fprintf(os.Stderr, `Usage: nimbus application <command> [flags]

Commands:
  list            List applications
  deploy          Deploy to target (bucket-based, or --ssh)
  delete          Delete an application
  watch           Watch for new deployments (runs on instance)
  install-watch   Install systemd watch service on instance
  disassociate    Remove an instance as a deployment target
`)
		os.Exit(1)
	}

	switch args[0] {
	case "list", "ls":
		handleList(args[1:])
	case "deploy":
		handleDeploy(args[1:])
	case "delete", "rm":
		handleDelete(args[1:])
	case "watch":
		handleWatch(args[1:])
	case "install-watch":
		handleInstallWatch(args[1:])
	case "disassociate":
		handleDisassociate(args[1:])
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
	useSSH := fs.Bool("ssh", false, "Use SSH/SCP deploy instead of bucket upload")
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

	if *useSSH {
		if err := deploy.Deploy(ctx, client, *name, *env, r); err != nil {
			fatal(err)
		}
	} else {
		if err := deploy.DeployViaBucket(ctx, client, *name, *env, r); err != nil {
			fatal(err)
		}
	}
}

func handleDelete(args []string) {
	fs := flag.NewFlagSet("nimbus application delete", flag.ExitOnError)
	name := fs.String("name", "", "Application name (required)")
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

	fmt.Printf("Deleting application %s...\n", *name)
	if err := applications.NewClient(client).Delete(ctx, *name, r); err != nil {
		fatal(err)
	}
	fmt.Printf("✅ Deleted application %s\n", *name)
}

func handleWatch(args []string) {
	fs := flag.NewFlagSet("nimbus application watch", flag.ExitOnError)
	name := fs.String("name", "", "Application name (required)")
	env := fs.String("env", "dev", "Environment name")
	region := fs.String("region", "", "AWS region (defaults to AWS_REGION or us-east-1)")
	interval := fs.Duration("interval", 10*time.Second, "Poll interval")
	keepPrevious := fs.Int("keep-previous", 10, "Number of previous deploy assets to keep")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		fs.Usage()
		os.Exit(1)
	}

	r := resolveRegion(*region)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := watch.Watch(ctx, *name, *env, r, *interval, *keepPrevious); err != nil {
		fatal(err)
	}
}

func handleInstallWatch(args []string) {
	fs := flag.NewFlagSet("nimbus application install-watch", flag.ExitOnError)
	name := fs.String("name", "", "Application name (required)")
	env := fs.String("env", "dev", "Environment name")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		fs.Usage()
		os.Exit(1)
	}

	fmt.Printf("Installing watch service for %s/%s...\n", *name, *env)
	if err := applications.InstallWatchService(*name, *env); err != nil {
		fatal(err)
	}
	fmt.Printf("✅ Installed and started nimbus-watch-%s-%s.service\n", *name, *env)
}

func handleDisassociate(args []string) {
	fs := flag.NewFlagSet("nimbus application disassociate", flag.ExitOnError)
	name := fs.String("name", "", "Application name (required)")
	env := fs.String("env", "dev", "Environment name")
	instance := fs.String("instance", "", "Instance name (required)")
	region := fs.String("region", "", "AWS region")
	noCleanup := fs.Bool("no-cleanup", false, "Skip stopping services and removing files on the instance")
	fs.Parse(args)

	if *name == "" || *instance == "" {
		fmt.Fprintln(os.Stderr, "Error: --name and --instance are required")
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

	cleanup := !*noCleanup
	fmt.Printf("Disassociating %s from %s/%s...\n", *instance, *name, *env)
	if err := applications.NewClient(client).RemoveTarget(ctx, *instance, *name, *env, r, cleanup); err != nil {
		fatal(err)
	}
	fmt.Printf("✅ Disassociated %s from %s/%s\n", *instance, *name, *env)
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
