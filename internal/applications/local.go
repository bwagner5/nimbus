package applications

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// LocalEnv represents an app/env discovered on the local instance.
type LocalEnv struct {
	App     string
	Env     string
	Status  string // running, stopped, unhealthy, not installed (watcher)
	Unit    string // systemd unit name
	Compose string // compose stack: up (N), down, no compose file
}

// LocalList scans /opt/nimbus for app/env directories and checks systemd + compose status.
func LocalList() ([]LocalEnv, error) {
	apps, err := os.ReadDir("/opt/nimbus")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []LocalEnv
	for _, app := range apps {
		if !app.IsDir() {
			continue
		}
		envs, err := os.ReadDir("/opt/nimbus/" + app.Name())
		if err != nil {
			continue
		}
		for _, env := range envs {
			if !env.IsDir() {
				continue
			}
			unit := fmt.Sprintf("nimbus-watch-%s-%s", app.Name(), env.Name())
			status := serviceStatus(unit)
			compose := composeStatus(app.Name(), env.Name())
			result = append(result, LocalEnv{
				App:     app.Name(),
				Env:     env.Name(),
				Status:  status,
				Unit:    unit + ".service",
				Compose: compose,
			})
		}
	}
	return result, nil
}

func composeStatus(appName, envName string) string {
	currentDir := fmt.Sprintf("/opt/nimbus/%s/%s/current", appName, envName)
	var composeFile string
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		p := currentDir + "/" + name
		if _, err := os.Stat(p); err == nil {
			composeFile = p
			break
		}
	}
	if composeFile == "" {
		return "no compose file"
	}
	out, err := exec.Command("docker", "compose", "-f", composeFile, "ps", "--format", "{{.State}}").Output()
	if err != nil {
		return "down"
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	running := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "running" {
			running++
		}
	}
	if running == 0 {
		return "down"
	}
	return fmt.Sprintf("up (%d)", running)
}

func serviceStatus(unit string) string {
	// Check if unit file exists
	path := fmt.Sprintf("/etc/systemd/system/%s.service", unit)
	if _, err := os.Stat(path); err != nil {
		return "not installed"
	}

	out, err := exec.Command("systemctl", "is-active", unit).Output()
	state := strings.TrimSpace(string(out))
	if err != nil {
		// is-active returns non-zero for inactive/failed
		switch state {
		case "failed":
			return "unhealthy"
		case "inactive":
			return "stopped"
		default:
			return "stopped"
		}
	}
	if state == "active" {
		return "running"
	}
	return state
}

// LocalDown fully tears down an app/env on this instance:
// 1. docker compose down on the current deployment
// 2. uninstall the watch service
// 3. remove the env directory (and the app directory if empty)
func LocalDown(appName, envName string) error {
	envDir := fmt.Sprintf("/opt/nimbus/%s/%s", appName, envName)
	currentDir := fmt.Sprintf("%s/current", envDir)

	// 1. Bring down running containers
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		p := currentDir + "/" + name
		if _, err := os.Stat(p); err == nil {
			down := exec.Command("docker", "compose", "-f", p, "down")
			down.Dir = currentDir
			down.Stdout = os.Stdout
			down.Stderr = os.Stderr
			down.Run() // best-effort
			break
		}
	}

	// 2. Uninstall watch service
	UninstallWatchService(appName, envName) // best-effort

	// 3. Remove directory
	if err := os.RemoveAll(envDir); err != nil {
		return fmt.Errorf("remove %s: %w", envDir, err)
	}
	appDir := fmt.Sprintf("/opt/nimbus/%s", appName)
	entries, err := os.ReadDir(appDir)
	if err != nil {
		return nil
	}
	if len(entries) == 0 {
		os.Remove(appDir)
	}
	return nil
}
