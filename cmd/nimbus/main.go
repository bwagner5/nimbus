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

// ── help rendering ──────────────────────────────────────────────────────

func bold(s string) string  { return "\033[1m" + s + "\033[0m" }
func dim(s string) string   { return "\033[2m" + s + "\033[0m" }
func cyan(s string) string  { return "\033[36m" + s + "\033[0m" }
func green(s string) string { return "\033[32m" + s + "\033[0m" }

type cmdEntry struct {
	name string
	desc string
}

func printHelp(header string, sections []struct {
	title   string
	entries []cmdEntry
}, footer string) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, bold(header))
	for _, sec := range sections {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "  %s\n", cyan(sec.title))
		for _, e := range sec.entries {
			fmt.Fprintf(os.Stderr, "    %-24s %s\n", green(e.name), e.desc)
		}
	}
	if footer != "" {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, dim(footer))
	}
	fmt.Fprintln(os.Stderr)
}

// ── root ────────────────────────────────────────────────────────────────

func rootHelp() {
	printHelp(
		"nimbus — a delightful cloud dashboard & deployment tool",
		[]struct {
			title   string
			entries []cmdEntry
		}{
			{
				title: "Interactive",
				entries: []cmdEntry{
					{"nimbus", "Launch the TUI dashboard"},
				},
			},
			{
				title: "Commands",
				entries: []cmdEntry{
					{"nimbus app <command>", "Manage applications & deployments"},
				},
			},
			{
				title: "Global Flags",
				entries: []cmdEntry{
					{"--trace", "Enable trace logging of TUI actions"},
					{"--trace-file <path>", "Path to trace output (default: nimbus-trace.log)"},
				},
			},
		},
		"  Run 'nimbus app --help' for application commands.",
	)
}

// ── app ─────────────────────────────────────────────────────────────────

func appHelp() {
	printHelp(
		"nimbus app — manage applications & deployments",
		[]struct {
			title   string
			entries []cmdEntry
		}{
			{
				title: "Commands",
				entries: []cmdEntry{
					{"list (ls)", "List applications in a region"},
					{"deploy", "Deploy to targets (bucket upload or SSH)"},
					{"rollback", "Roll back to the previous deployment"},
					{"promote", "Promote a deploy from one env to another"},
					{"env", "Manage environments (add, list, reorder)"},
					{"delete (rm)", "Delete an application and its resources"},
					{"disassociate", "Remove an instance as a deployment target"},
				},
			},
			{
				title: "On-Instance",
				entries: []cmdEntry{
					{"local", "Commands that run on a deployment target instance"},
				},
			},
		},
		"  Run 'nimbus app <command> --help' for command-specific flags.\n  Run 'nimbus app local --help' for on-instance commands.",
	)
}

// ── leaf command help (flag sets) ───────────────────────────────────────

func flagHelp(fs *flag.FlagSet, usage, description string) {
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, bold(usage))
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, " ", description)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "  %s\n", cyan("Flags"))
		fs.VisitAll(func(f *flag.Flag) {
			def := ""
			if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "0s" {
				def = dim(fmt.Sprintf(" (default: %s)", f.DefValue))
			}
			fmt.Fprintf(os.Stderr, "    %-24s %s%s\n", green("--"+f.Name), f.Usage, def)
		})
		fmt.Fprintln(os.Stderr)
	}
}

// ── main ────────────────────────────────────────────────────────────────

func main() {
	if len(os.Args) < 2 {
		handleTUI(os.Args[1:])
		return
	}
	cmd := os.Args[1]
	if cmd == "--help" || cmd == "-h" || cmd == "help" {
		rootHelp()
		return
	}
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
	fs.Usage = func() { rootHelp() }
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
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		appHelp()
		if len(args) == 0 {
			os.Exit(1)
		}
		return
	}

	switch args[0] {
	case "list", "ls":
		handleList(args[1:])
	case "deploy":
		handleDeploy(args[1:])
	case "rollback":
		handleRollback(args[1:])
	case "delete", "rm":
		handleDelete(args[1:])
	case "local":
		handleLocal(args[1:])
	case "disassociate":
		handleDisassociate(args[1:])
	case "env":
		handleEnv(args[1:])
	case "promote":
		handlePromote(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: nimbus app %s\n", args[0])
		os.Exit(1)
	}
}

func handleList(args []string) {
	fs := flag.NewFlagSet("nimbus app list", flag.ExitOnError)
	region := fs.String("region", "", "AWS region (defaults to AWS_REGION or us-east-1)")
	flagHelp(fs, "nimbus app list — list applications", "Show all applications in a region with their state, bucket, and region.")
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
	fs := flag.NewFlagSet("nimbus app deploy", flag.ExitOnError)
	name := fs.String("name", "", "Application name")
	env := fs.String("env", "", "Environment name")
	region := fs.String("region", "", "AWS region (defaults to AWS_REGION or us-east-1)")
	useSSH := fs.Bool("ssh", false, "Use SSH/SCP deploy instead of bucket upload")
	flagHelp(fs, "nimbus app deploy — deploy to targets", "Push the current directory to deployment targets via S3 bucket or SSH.\nWithout --ssh, runs an interactive deploy wizard.")
	fs.Parse(args)

	r := resolveRegion(*region)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := aws.NewClient(ctx, r)
	if err != nil {
		fatal(err)
	}

	if *useSSH {
		if *name == "" {
			fmt.Fprintln(os.Stderr, "Error: --name is required for SSH deploy")
			os.Exit(1)
		}
		if *env == "" {
			*env = "dev"
		}
		if err := deploy.Deploy(ctx, client, *name, *env, r); err != nil {
			fatal(err)
		}
	} else {
		if err := deploy.RunInteractiveDeploy(ctx, client, *name, *env, r); err != nil {
			fatal(err)
		}
	}
}

func handleRollback(args []string) {
	fs := flag.NewFlagSet("nimbus app rollback", flag.ExitOnError)
	name := fs.String("name", "", "Application name")
	env := fs.String("env", "", "Environment name")
	region := fs.String("region", "", "AWS region (defaults to AWS_REGION or us-east-1)")
	flagHelp(fs, "nimbus app rollback — roll back to previous deployment", "Copy the previous deploy asset to a new key with the current timestamp,\ncreating a roll-forward entry that the watch service picks up automatically.\nIf --name/--env are omitted, prompts for selection.")
	fs.Parse(args)

	r := resolveRegion(*region)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := aws.NewClient(ctx, r)
	if err != nil {
		fatal(err)
	}

	app, err := selectApp(ctx, client, *name, r)
	if err != nil {
		fatal(err)
	}
	envName := selectEnv(app, *env)

	if err := deploy.Rollback(ctx, client, app.Name, envName, r); err != nil {
		fatal(err)
	}
}

func handleDelete(args []string) {
	fs := flag.NewFlagSet("nimbus app delete", flag.ExitOnError)
	name := fs.String("name", "", "Application name")
	region := fs.String("region", "", "AWS region (defaults to AWS_REGION or us-east-1)")
	flagHelp(fs, "nimbus app delete — delete an application", "Remove an application and all its associated resources (buckets, tags, instance configs).\nIf --name is omitted, prompts for selection.")
	fs.Parse(args)

	r := resolveRegion(*region)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := aws.NewClient(ctx, r)
	if err != nil {
		fatal(err)
	}

	app, err := selectApp(ctx, client, *name, r)
	if err != nil {
		fatal(err)
	}

	fmt.Printf("Deleting application %s...\n", app.Name)
	if err := applications.NewClient(client).Delete(ctx, app.Name, r); err != nil {
		fatal(err)
	}
	fmt.Printf("✅ Deleted application %s\n", app.Name)
}

func handleWatch(args []string) {
	fs := flag.NewFlagSet("nimbus app local watch", flag.ExitOnError)
	name := fs.String("name", "", "Application name (required)")
	env := fs.String("env", "dev", "Environment name")
	region := fs.String("region", "", "AWS region (defaults to AWS_REGION or us-east-1)")
	interval := fs.Duration("interval", 10*time.Second, "Poll interval")
	keepPrevious := fs.Int("keep-previous", 10, "Number of previous deploy assets to keep")
	flagHelp(fs, "nimbus app local watch — watch for deployments", "Poll the deploy bucket and automatically apply new assets.\nTypically managed via 'nimbus app local up' as a systemd service.")
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

func handleUp(args []string) {
	fs := flag.NewFlagSet("nimbus app local up", flag.ExitOnError)
	name := fs.String("name", "", "Application name (required)")
	env := fs.String("env", "dev", "Environment name")
	flagHelp(fs, "nimbus app local up — start watching for deployments", "Create the app/env directory and install a systemd service that watches for new deployments.")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		fs.Usage()
		os.Exit(1)
	}

	fmt.Printf("Starting %s/%s...\n", *name, *env)
	if err := applications.InstallWatchService(*name, *env); err != nil {
		fatal(err)
	}
	fmt.Printf("✅ Installed and started nimbus-watch-%s-%s.service\n", *name, *env)
}

func handleLocal(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printHelp(
			"nimbus app local — on-instance commands",
			[]struct {
				title   string
				entries []cmdEntry
			}{
				{
					title: "Lifecycle",
					entries: []cmdEntry{
						{"up", "Start watching for deployments (install watch service)"},
						{"down", "Stop containers, uninstall watch service, clean up files"},
					},
				},
				{
					title: "Inspection",
					entries: []cmdEntry{
						{"list (ls)", "List app environments on this instance"},
						{"watch", "Poll for new deploys and apply them (low-level)"},
					},
				},
			},
			"  Run 'nimbus app local <command> --help' for command-specific flags.",
		)
		if len(args) == 0 {
			os.Exit(1)
		}
		return
	}
	switch args[0] {
	case "up":
		handleUp(args[1:])
	case "down":
		handleDown(args[1:])
	case "list", "ls":
		handleLocalList(args[1:])
	case "watch":
		handleWatch(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: nimbus app local %s\n", args[0])
		os.Exit(1)
	}
}

func handleLocalList(args []string) {
	fs := flag.NewFlagSet("nimbus app local list", flag.ExitOnError)
	flagHelp(fs, "nimbus app local list — list local environments", "Show app environments deployed to /opt/nimbus on this instance.")
	fs.Parse(args)

	envs, err := applications.LocalList()
	if err != nil {
		fatal(err)
	}
	if len(envs) == 0 {
		fmt.Println("No application environments found in /opt/nimbus")
		return
	}
	fmt.Printf("%-20s %-12s %-14s %s\n", "APP", "ENV", "STATUS", "UNIT")
	for _, e := range envs {
		fmt.Printf("%-20s %-12s %-14s %s\n", e.App, e.Env, e.Status, e.Unit)
	}
}

func handleDown(args []string) {
	fs := flag.NewFlagSet("nimbus app local down", flag.ExitOnError)
	name := fs.String("name", "", "Application name (required)")
	env := fs.String("env", "dev", "Environment name")
	flagHelp(fs, "nimbus app local down — stop and clean up a deployment", "Stop running containers, uninstall the watch service, and remove the app/env directory.")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		fs.Usage()
		os.Exit(1)
	}

	fmt.Printf("Stopping %s/%s...\n", *name, *env)
	if err := applications.LocalDown(*name, *env); err != nil {
		fatal(err)
	}
	fmt.Printf("✅ Stopped and cleaned up %s/%s\n", *name, *env)
}

func handleDisassociate(args []string) {
	fs := flag.NewFlagSet("nimbus app disassociate", flag.ExitOnError)
	name := fs.String("name", "", "Application name")
	env := fs.String("env", "", "Environment name")
	instance := fs.String("instance", "", "Instance name")
	region := fs.String("region", "", "AWS region")
	noCleanup := fs.Bool("no-cleanup", false, "Skip running 'nimbus app local down' on the instance")
	flagHelp(fs, "nimbus app disassociate — remove a deployment target", "Untag an instance and run 'nimbus app local down' to clean up.\nIf --name/--instance are omitted, prompts for selection.")
	fs.Parse(args)

	r := resolveRegion(*region)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := aws.NewClient(ctx, r)
	if err != nil {
		fatal(err)
	}

	app, err := selectApp(ctx, client, *name, r)
	if err != nil {
		fatal(err)
	}
	envName := selectEnv(app, *env)
	instName, err := selectTarget(ctx, client, app.Name, envName, r, *instance)
	if err != nil {
		fatal(err)
	}

	cleanup := !*noCleanup
	fmt.Printf("Disassociating %s from %s/%s...\n", instName, app.Name, envName)
	if err := applications.NewClient(client).RemoveTarget(ctx, instName, app.Name, envName, r, cleanup); err != nil {
		fatal(err)
	}
	fmt.Printf("✅ Disassociated %s from %s/%s\n", instName, app.Name, envName)
}

func handleEnv(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printHelp("nimbus app env — manage environments", []struct {
			title   string
			entries []cmdEntry
		}{{title: "Commands", entries: []cmdEntry{
			{"add", "Add a new environment to an application"},
			{"list (ls)", "List environments in order"},
			{"reorder", "Set environment order"},
		}}}, "")
		return
	}
	switch args[0] {
	case "add":
		handleEnvAdd(args[1:])
	case "list", "ls":
		handleEnvList(args[1:])
	case "reorder":
		handleEnvReorder(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: nimbus app env %s\n", args[0])
		os.Exit(1)
	}
}

func handleEnvAdd(args []string) {
	fs := flag.NewFlagSet("nimbus app env add", flag.ExitOnError)
	name := fs.String("name", "", "Application name")
	env := fs.String("env", "", "New environment name")
	region := fs.String("region", "", "AWS region")
	flagHelp(fs, "nimbus app env add — add a new environment", "Create a new environment bucket and update the environment order.")
	fs.Parse(args)

	r := resolveRegion(*region)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := aws.NewClient(ctx, r)
	if err != nil {
		fatal(err)
	}

	app, err := selectApp(ctx, client, *name, r)
	if err != nil {
		fatal(err)
	}

	envName := *env
	if envName == "" {
		fmt.Fprintf(os.Stderr, "Environment name: ")
		fmt.Scan(&envName)
	}
	if envName == "" {
		fmt.Fprintln(os.Stderr, "Error: environment name is required")
		os.Exit(1)
	}

	fmt.Printf("Adding environment %s to %s...\n", envName, app.Name)
	if err := applications.NewClient(client).AddEnvironment(ctx, app.Name, envName, r); err != nil {
		fatal(err)
	}
	fmt.Printf("✅ Added environment %s to %s\n", envName, app.Name)
}

func handleEnvList(args []string) {
	fs := flag.NewFlagSet("nimbus app env list", flag.ExitOnError)
	name := fs.String("name", "", "Application name")
	region := fs.String("region", "", "AWS region")
	flagHelp(fs, "nimbus app env list — list environments in order", "Show the environment pipeline order for an application.")
	fs.Parse(args)

	r := resolveRegion(*region)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := aws.NewClient(ctx, r)
	if err != nil {
		fatal(err)
	}

	app, err := selectApp(ctx, client, *name, r)
	if err != nil {
		fatal(err)
	}

	order, err := applications.NewClient(client).GetEnvOrder(ctx, app.Name, r)
	if err != nil {
		fatal(err)
	}
	if len(order) == 0 {
		fmt.Println("No environments found")
		return
	}
	for i, e := range order {
		fmt.Printf("  %d. %s\n", i+1, e)
	}
}

func handleEnvReorder(args []string) {
	fs := flag.NewFlagSet("nimbus app env reorder", flag.ExitOnError)
	name := fs.String("name", "", "Application name")
	order := fs.String("order", "", "Comma-separated environment order (e.g. dev,staging,prod)")
	region := fs.String("region", "", "AWS region")
	flagHelp(fs, "nimbus app env reorder — set environment order", "Set the pipeline order for environments. If --order is omitted, shows current order and prompts.")
	fs.Parse(args)

	r := resolveRegion(*region)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := aws.NewClient(ctx, r)
	if err != nil {
		fatal(err)
	}

	app, err := selectApp(ctx, client, *name, r)
	if err != nil {
		fatal(err)
	}

	appClient := applications.NewClient(client)
	var newOrder []string
	if *order != "" {
		newOrder = strings.Split(*order, ",")
	} else {
		current, err := appClient.GetEnvOrder(ctx, app.Name, r)
		if err != nil {
			fatal(err)
		}
		fmt.Println("Current order:")
		for i, e := range current {
			fmt.Printf("  %d. %s\n", i+1, e)
		}
		fmt.Fprintf(os.Stderr, "New order (comma-separated): ")
		var input string
		fmt.Scan(&input)
		newOrder = strings.Split(input, ",")
	}

	if len(newOrder) == 0 {
		fmt.Fprintln(os.Stderr, "Error: order must not be empty")
		os.Exit(1)
	}

	if err := appClient.SetEnvOrder(ctx, app.Name, r, newOrder); err != nil {
		fatal(err)
	}
	fmt.Println("✅ Environment order updated")
}

func handlePromote(args []string) {
	fs := flag.NewFlagSet("nimbus app promote", flag.ExitOnError)
	name := fs.String("name", "", "Application name")
	from := fs.String("from", "", "Source environment")
	to := fs.String("to", "", "Destination environment")
	region := fs.String("region", "", "AWS region")
	flagHelp(fs, "nimbus app promote — promote a deploy between environments", "Copy the latest deploy asset from one environment to another.\nIf flags are omitted, prompts for selection.")
	fs.Parse(args)

	r := resolveRegion(*region)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client, err := aws.NewClient(ctx, r)
	if err != nil {
		fatal(err)
	}

	app, err := selectApp(ctx, client, *name, r)
	if err != nil {
		fatal(err)
	}

	appClient := applications.NewClient(client)
	order, err := appClient.GetEnvOrder(ctx, app.Name, r)
	if err != nil {
		fatal(err)
	}
	if len(order) < 2 {
		fatal(fmt.Errorf("need at least 2 environments to promote, found %d", len(order)))
	}

	srcEnv := *from
	if srcEnv == "" {
		idx := promptSelect("Source environment", order)
		srcEnv = order[idx]
	}

	destEnv := *to
	if destEnv == "" {
		// Default to next env in order
		var destOptions []string
		for _, e := range order {
			if e != srcEnv {
				destOptions = append(destOptions, e)
			}
		}
		if len(destOptions) == 0 {
			fatal(fmt.Errorf("no destination environments available"))
		}
		idx := promptSelect("Destination environment", destOptions)
		destEnv = destOptions[idx]
	}

	if err := deploy.Promote(ctx, client, app.Name, srcEnv, destEnv, r); err != nil {
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
