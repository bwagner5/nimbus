package applications

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// LocalEnv represents an app/env discovered on the local instance.
type LocalEnv struct {
	App    string
	Env    string
	Status string // running, stopped, unhealthy, not installed
	Unit   string // systemd unit name
}

// LocalList scans /opt/nimbus for app/env directories and checks systemd status.
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
			result = append(result, LocalEnv{
				App:    app.Name(),
				Env:    env.Name(),
				Status: status,
				Unit:   unit + ".service",
			})
		}
	}
	return result, nil
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

// LocalRemove removes the env directory for an app/env and the app directory if empty.
func LocalRemove(appName, envName string) error {
	envDir := fmt.Sprintf("/opt/nimbus/%s/%s", appName, envName)
	if err := os.RemoveAll(envDir); err != nil {
		return fmt.Errorf("remove %s: %w", envDir, err)
	}
	appDir := fmt.Sprintf("/opt/nimbus/%s", appName)
	entries, err := os.ReadDir(appDir)
	if err != nil {
		return nil // already gone
	}
	if len(entries) == 0 {
		os.Remove(appDir)
	}
	return nil
}
