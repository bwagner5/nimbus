package deploy

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

// Deploy tars the current directory, SCPs it to the target instance, and runs docker compose up --build.
func Deploy(ctx context.Context, client *aws.Client, appName, envName, region string) error {
	composeFile := findComposeFile()
	if composeFile == "" {
		return fmt.Errorf("no docker-compose.yml or compose.yaml found in current directory")
	}

	appClient := applications.NewClient(client)

	fmt.Println("🔍 Finding target instance...")
	target, err := appClient.FindTarget(ctx, appName, envName, region)
	if err != nil {
		return err
	}
	fmt.Printf("🎯 Target: %s (%s)\n", target.Name, target.IP)

	commitID := getGitCommit()
	assetName := fmt.Sprintf("%d-%s.tar.gz", time.Now().Unix(), commitID)
	tmpFile, err := os.CreateTemp("", "nimbus-deploy-*.tar.gz")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	fmt.Println("📦 Packaging source directory...")
	if err := tarDir(".", tmpFile); err != nil {
		tmpFile.Close()
		return fmt.Errorf("create archive: %w", err)
	}
	tmpFile.Close()

	fmt.Println("🔑 Getting SSH credentials...")
	creds, err := appClient.GetSSHCredentials(ctx, target.Name, region)
	if err != nil {
		return err
	}
	defer os.Remove(creds.KeyPath)
	defer os.Remove(creds.KeyPath + "-cert.pub")

	remotePath := "/tmp/" + assetName
	deployDir := fmt.Sprintf("/opt/nimbus/%s/%s", appName, envName)
	sshTarget := fmt.Sprintf("%s@%s", creds.Username, target.IP)
	sshOpts := []string{"-i", creds.KeyPath, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}

	fmt.Printf("📤 Uploading to %s...\n", target.Name)
	scpArgs := append(sshOpts, tmpPath, sshTarget+":"+remotePath)
	if err := runCmd("scp", scpArgs...); err != nil {
		return fmt.Errorf("scp: %w", err)
	}

	fmt.Println("🚀 Deploying on instance...")
	remoteCmd := fmt.Sprintf(
		"set -e && sudo mkdir -p %s && sudo tar xzf %s -C %s && cd %s && sudo docker compose up --build -d && rm -f %s",
		deployDir, remotePath, deployDir, deployDir, remotePath,
	)
	sshArgs := append(sshOpts, sshTarget, remoteCmd)
	if err := runCmd("ssh", sshArgs...); err != nil {
		return fmt.Errorf("remote deploy: %w", err)
	}

	fmt.Printf("✅ Deployed %s to %s/%s\n", assetName, appName, envName)
	return nil
}

func tarDir(srcDir string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		base := filepath.Base(path)
		if info.IsDir() && (base == ".git" || base == "node_modules" || base == ".nimbus") {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		hdr := &tar.Header{
			Name: rel,
			Size: info.Size(),
			Mode: int64(info.Mode()),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

func findComposeFile() string {
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if _, err := os.Stat(name); err == nil {
			return name
		}
	}
	return ""
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func getGitCommit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "nocommit"
	}
	return strings.TrimSpace(string(out))
}
