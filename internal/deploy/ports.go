package deploy

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// ParseComposePorts reads a compose file and returns the publicly exposed host ports.
// It parses the "ports:" sections looking for host:container mappings.
func ParseComposePorts(path string) ([]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	seen := map[int]bool{}
	var ports []int
	inPorts := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Detect "ports:" key
		if strings.HasPrefix(trimmed, "ports:") {
			inPorts = true
			continue
		}

		// If in ports section, parse list items
		if inPorts {
			if !strings.HasPrefix(trimmed, "-") {
				inPorts = false
				continue
			}
			port := parsePortEntry(strings.TrimPrefix(trimmed, "-"))
			if port > 0 && !seen[port] {
				seen[port] = true
				ports = append(ports, port)
			}
			continue
		}
	}
	return ports, scanner.Err()
}

// parsePortEntry extracts the host port from a compose port string.
// Formats: "8080", "8080:80", "8080:80/tcp", "0.0.0.0:8080:80", etc.
func parsePortEntry(s string) int {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'")

	// Strip protocol suffix
	if idx := strings.Index(s, "/"); idx >= 0 {
		s = s[:idx]
	}

	parts := strings.Split(s, ":")
	var hostPort string
	switch len(parts) {
	case 1:
		hostPort = parts[0] // "8080"
	case 2:
		hostPort = parts[0] // "8080:80"
	case 3:
		hostPort = parts[1] // "0.0.0.0:8080:80"
	default:
		return 0
	}

	// Handle ranges like "8080-8090" — take the start
	if idx := strings.Index(hostPort, "-"); idx >= 0 {
		hostPort = hostPort[:idx]
	}

	p, err := strconv.Atoi(hostPort)
	if err != nil || p <= 0 {
		return 0
	}
	return p
}
