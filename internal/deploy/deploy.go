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

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	lstypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

const tagPrefix = "nimbus:app:"

// Deploy tars the current directory, SCPs it to the target instance, and runs docker compose up --build.
func Deploy(ctx context.Context, client *aws.Client, appName, envName, region string) error {
	composeFile := findComposeFile()
	if composeFile == "" {
		return fmt.Errorf("no docker-compose.yml or compose.yaml found in current directory")
	}

	fmt.Println("🔍 Finding target instance...")
	inst, err := findTargetInstance(ctx, client, appName, envName, region)
	if err != nil {
		return err
	}
	fmt.Printf("🎯 Target: %s (%s)\n", inst.Name, inst.IP)

	// Create tar.gz of current directory
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
	creds, err := getSSHCredentials(ctx, client, inst.Name, region)
	if err != nil {
		return err
	}
	defer os.Remove(creds.keyPath)
	defer os.Remove(creds.keyPath + "-cert.pub")

	remotePath := "/tmp/" + assetName
	deployDir := fmt.Sprintf("/opt/nimbus/%s/%s", appName, envName)
	sshTarget := fmt.Sprintf("%s@%s", creds.username, inst.IP)
	sshOpts := []string{"-i", creds.keyPath, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}

	fmt.Printf("📤 Uploading to %s...\n", inst.Name)
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
		// Skip .git and other common non-deploy dirs
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

type targetInstance struct {
	Name string
	IP   string
}

func findTargetInstance(ctx context.Context, client *aws.Client, appName, envName, region string) (*targetInstance, error) {
	svc := lightsail.NewFromConfig(client.WithRegion(region).Config())
	tagKey := fmt.Sprintf("%s%s:%s", tagPrefix, appName, envName)

	var pageToken *string
	for {
		out, err := svc.GetInstances(ctx, &lightsail.GetInstancesInput{PageToken: pageToken})
		if err != nil {
			return nil, fmt.Errorf("list instances: %w", err)
		}
		for _, inst := range out.Instances {
			for _, tag := range inst.Tags {
				if tag.Key != nil && *tag.Key == tagKey {
					ip := ""
					if inst.PublicIpAddress != nil {
						ip = *inst.PublicIpAddress
					}
					if ip == "" {
						continue
					}
					return &targetInstance{Name: *inst.Name, IP: ip}, nil
				}
			}
		}
		if out.NextPageToken == nil {
			break
		}
		pageToken = out.NextPageToken
	}
	return nil, fmt.Errorf("no instance found with tag %s", tagKey)
}

type sshCredentials struct {
	keyPath  string
	username string
}

func getSSHCredentials(ctx context.Context, client *aws.Client, instanceName, region string) (*sshCredentials, error) {
	svc := lightsail.NewFromConfig(client.WithRegion(region).Config())
	out, err := svc.GetInstanceAccessDetails(ctx, &lightsail.GetInstanceAccessDetailsInput{
		InstanceName: &instanceName,
		Protocol:     lstypes.InstanceAccessProtocolSsh,
	})
	if err != nil {
		return nil, fmt.Errorf("get SSH access: %w", err)
	}
	d := out.AccessDetails

	keyFile, err := os.CreateTemp("", "nimbus-ssh-*")
	if err != nil {
		return nil, err
	}
	keyFile.Chmod(0600)
	keyFile.WriteString(*d.PrivateKey)
	keyFile.Close()

	if d.CertKey != nil && *d.CertKey != "" {
		os.WriteFile(keyFile.Name()+"-cert.pub", []byte(*d.CertKey), 0600)
	}

	return &sshCredentials{keyPath: keyFile.Name(), username: *d.Username}, nil
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
