// Package watch implements the deployment watch loop that runs on Lightsail instances.
package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/wagnerbm/nimbusv2/internal/applications"
)

const deployPrefix = "deploy/"

// Watch runs the deployment watch loop. It polls the bucket for new deploy assets,
// downloads and applies them, and uploads status on change or every minute.
func Watch(ctx context.Context, appName, envName, region string, interval time.Duration, keepPrevious int) error {
	log.Printf("Starting watch for %s/%s in %s (interval=%s, keep=%d)", appName, envName, region, interval, keepPrevious)

	baseDir := fmt.Sprintf("/opt/nimbus/%s/%s", appName, envName)

	// Read bucket name from .bucket file (written by add-target)
	bucketData, err := os.ReadFile(filepath.Join(baseDir, ".bucket"))
	if err != nil {
		return fmt.Errorf("read .bucket file: %w (run add-target first)", err)
	}
	bucketName := strings.TrimSpace(string(bucketData))
	if bucketName == "" {
		return fmt.Errorf(".bucket file is empty")
	}
	log.Printf("Bucket: %s", bucketName)

	// AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY come from env (via systemd EnvironmentFile)
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	s3svc := s3.NewFromConfig(cfg)

	instanceName, _ := getInstanceName()
	if instanceName == "" {
		h, _ := os.Hostname()
		instanceName = h
	}

	currentDir := filepath.Join(baseDir, "current")
	lastDeployFile := filepath.Join(baseDir, ".last-deploy")

	os.MkdirAll(baseDir, 0755)

	lastKey := ""
	if data, err := os.ReadFile(lastDeployFile); err == nil {
		lastKey = strings.TrimSpace(string(data))
	}

	var lastStatus string
	lastStatusUpload := time.Time{}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Watch stopped")
			return nil
		case <-ticker.C:
		}

		// List deploy assets
		latest, allKeys, err := findLatestDeploy(ctx, s3svc, bucketName)
		if err != nil {
			log.Printf("Error listing deploys: %v", err)
			continue
		}

		// Deploy if new asset found
		if latest != "" && latest != lastKey {
			log.Printf("New deploy found: %s", latest)
			if err := pullAndDeploy(ctx, s3svc, bucketName, latest, baseDir, currentDir); err != nil {
				log.Printf("Deploy failed: %v", err)
			} else {
				lastKey = latest
				os.WriteFile(lastDeployFile, []byte(latest), 0644)
				log.Printf("Deploy complete: %s", latest)

				// Prune old assets
				pruneOldDeploys(ctx, s3svc, bucketName, allKeys, keepPrevious)
			}
		}

		// Build and conditionally upload status
		status := buildStatus(instanceName, bucketName, region, lastKey, currentDir)
		statusJSON, _ := json.Marshal(status)
		statusStr := string(statusJSON)

		if statusStr != lastStatus || time.Since(lastStatusUpload) > time.Minute {
			statusKey := instanceName + "_status.json"
			s3svc.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      &bucketName,
				Key:         &statusKey,
				Body:        bytes.NewReader(statusJSON),
				ContentType: strPtr("application/json"),
			})
			lastStatus = statusStr
			lastStatusUpload = time.Now()
		}
	}
}

func findLatestDeploy(ctx context.Context, svc *s3.Client, bucket string) (latest string, allKeys []string, err error) {
	prefix := deployPrefix
	out, err := svc.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	})
	if err != nil {
		return "", nil, err
	}
	if len(out.Contents) == 0 {
		return "", nil, nil
	}

	// Sort by key (timestamp prefix ensures chronological order)
	sort.Slice(out.Contents, func(i, j int) bool {
		return *out.Contents[i].Key > *out.Contents[j].Key
	})

	for _, obj := range out.Contents {
		allKeys = append(allKeys, *obj.Key)
	}
	return *out.Contents[0].Key, allKeys, nil
}

func pullAndDeploy(ctx context.Context, svc *s3.Client, bucket, key, baseDir, currentDir string) error {
	// Download
	out, err := svc.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return fmt.Errorf("download %s: %w", key, err)
	}
	defer out.Body.Close()

	stagingDir := filepath.Join(baseDir, "staging")
	os.RemoveAll(stagingDir)
	os.MkdirAll(stagingDir, 0755)

	tmpFile := filepath.Join(baseDir, "deploy.tar.gz")
	f, err := os.Create(tmpFile)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, out.Body); err != nil {
		f.Close()
		return err
	}
	f.Close()
	defer os.Remove(tmpFile)

	// Extract
	cmd := exec.CommandContext(ctx, "tar", "xzf", tmpFile, "-C", stagingDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extract: %s: %w", output, err)
	}

	// Compose down on old deployment
	if composeFile := findCompose(currentDir); composeFile != "" {
		down := exec.CommandContext(ctx, "docker", "compose", "-f", composeFile, "down")
		down.Dir = currentDir
		down.Run() // best-effort
	}

	// Swap staging → current
	os.RemoveAll(currentDir)
	if err := os.Rename(stagingDir, currentDir); err != nil {
		return fmt.Errorf("swap staging to current: %w", err)
	}

	// Compose up
	composeFile := findCompose(currentDir)
	if composeFile == "" {
		return fmt.Errorf("no compose file in deployed asset")
	}
	up := exec.CommandContext(ctx, "docker", "compose", "-f", composeFile, "up", "--build", "-d")
	up.Dir = currentDir
	up.Stdout = os.Stdout
	up.Stderr = os.Stderr
	return up.Run()
}

func buildStatus(instanceName, bucket, region, lastKey, currentDir string) *applications.InstanceStatus {
	status := &applications.InstanceStatus{
		Instance:  instanceName,
		Timestamp: time.Now().UTC(),
		Status:    "idle",
	}

	if lastKey != "" {
		objectURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, region, lastKey)
		// Parse timestamp from key: deploy/<unix>-<commit>.tar.gz
		var ts time.Time
		parts := strings.TrimPrefix(lastKey, deployPrefix)
		if idx := strings.Index(parts, "-"); idx > 0 {
			if unix, err := fmt.Sscanf(parts[:idx], "%d", new(int64)); err == nil && unix == 1 {
				var epoch int64
				fmt.Sscanf(parts[:idx], "%d", &epoch)
				ts = time.Unix(epoch, 0).UTC()
			}
		}
		status.LastDeploy = &applications.DeployInfo{Timestamp: ts, ObjectURL: objectURL}
	}

	// Get container info
	containers, endpoints := getContainerInfo(currentDir)
	status.Containers = containers
	status.Endpoints = endpoints

	// Derive overall status
	running := 0
	for _, c := range containers {
		if c.Status == "running" {
			running++
		}
	}
	switch {
	case len(containers) == 0:
		status.Status = "idle"
	case running == len(containers):
		status.Status = "healthy"
	case running > 0:
		status.Status = "degraded"
	default:
		status.Status = "down"
	}

	return status
}

// dockerPSEntry matches the JSON output of `docker compose ps --format json`.
type dockerPSEntry struct {
	Name       string `json:"Name"`
	Image      string `json:"Image"`
	State      string `json:"State"`
	CreatedAt  string `json:"CreatedAt"`
	Publishers []struct {
		PublishedPort int `json:"PublishedPort"`
	} `json:"Publishers"`
}

func getContainerInfo(currentDir string) ([]applications.ContainerStatus, []string) {
	composeFile := findCompose(currentDir)
	if composeFile == "" {
		return nil, nil
	}

	cmd := exec.Command("docker", "compose", "-f", composeFile, "ps", "--format", "json")
	cmd.Dir = currentDir
	output, err := cmd.Output()
	if err != nil {
		return nil, nil
	}

	publicIP := getPublicIP()

	var containers []applications.ContainerStatus
	var endpoints []string
	seenPorts := map[int]bool{}

	// docker compose ps --format json outputs one JSON object per line
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		var entry dockerPSEntry
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		var startedAt time.Time
		if t, err := time.Parse("2006-01-02 15:04:05 -0700 MST", entry.CreatedAt); err == nil {
			startedAt = t.UTC()
		}
		containers = append(containers, applications.ContainerStatus{
			Name:      entry.Name,
			Image:     entry.Image,
			Status:    entry.State,
			StartedAt: startedAt,
		})
		if publicIP != "" {
			for _, p := range entry.Publishers {
				if p.PublishedPort > 0 && !seenPorts[p.PublishedPort] {
					seenPorts[p.PublishedPort] = true
					endpoints = append(endpoints, fmt.Sprintf("http://%s:%d", publicIP, p.PublishedPort))
				}
			}
		}
	}
	return containers, endpoints
}

func getPublicIP() string {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://169.254.169.254/latest/meta-data/public-ipv4")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(data))
}

func getInstanceName() (string, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://169.254.169.254/latest/meta-data/tags/instance/Name")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("metadata returned %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(data)), nil
}

func findCompose(dir string) string {
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func pruneOldDeploys(ctx context.Context, svc *s3.Client, bucket string, allKeys []string, keep int) {
	if len(allKeys) <= keep {
		return
	}
	toDelete := allKeys[keep:]
	var objects []s3types.ObjectIdentifier
	for _, k := range toDelete {
		k := k
		objects = append(objects, s3types.ObjectIdentifier{Key: &k})
	}
	svc.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: &bucket,
		Delete: &s3types.Delete{Objects: objects},
	})
	log.Printf("Pruned %d old deploy assets", len(objects))
}

func strPtr(s string) *string { return &s }
