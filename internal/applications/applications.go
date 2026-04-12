// Package applications provides the Lightsail Application SDK for managing
// nimbus applications (buckets, instance tags, SSH access).
package applications

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	lstypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

const (
	BucketPrefix = "nimbus--"
	TagPrefix    = "nimbus:app:"
)

// App represents a discovered nimbus application.
type App struct {
	Name   string
	Bucket string // first env bucket (for backward compat display)
	Region string
	State  string
	Envs   []string
}

// Environment represents an app environment parsed from instance tags.
type Environment struct {
	Name    string
	Bucket  string
	Targets []Target
}

// Target is a Lightsail instance associated with an app/env.
type Target struct {
	Name           string
	State          string
	IP             string
	Region         string
	InstanceStatus *InstanceStatus
}

// InstanceStatus is the status file uploaded by the watch process.
type InstanceStatus struct {
	Instance   string            `json:"instance"`
	Timestamp  time.Time         `json:"timestamp"`
	Status     string            `json:"status"`
	LastDeploy *DeployInfo       `json:"last_deploy"`
	Containers []ContainerStatus `json:"containers"`
	Endpoints  []string          `json:"endpoints"`
}

// DeployInfo describes the last deployed asset.
type DeployInfo struct {
	Timestamp time.Time `json:"timestamp"`
	ObjectURL string    `json:"object_url"`
}

// ContainerStatus describes a running container.
type ContainerStatus struct {
	Name      string    `json:"name"`
	Image     string    `json:"image"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
}

// Detail holds full application detail including environments.
type Detail struct {
	App
	Environments []Environment
}

// SSHCredentials holds temporary SSH key material for an instance.
type SSHCredentials struct {
	KeyPath  string
	Username string
	IP       string
}

// Client wraps the AWS client for application operations.
type Client struct {
	aws *aws.Client
}

// NewClient creates a new applications client.
func NewClient(c *aws.Client) *Client {
	return &Client{aws: c}
}

// AccountID returns the AWS account ID of the caller.
func (c *Client) AccountID(ctx context.Context) (string, error) {
	svc := sts.NewFromConfig(c.aws.Config())
	out, err := svc.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("get account id: %w", err)
	}
	return *out.Account, nil
}

// BucketName returns the bucket name for an app-env: nimbus--<account>--<app>--<env>.
func BucketName(accountID, appName, envName string) string {
	return fmt.Sprintf("%s%s--%s--%s", BucketPrefix, accountID, appName, envName)
}

// ParseAppName extracts just the app name from a bucket name.
// ParseAppName extracts just the app name from a bucket name.
func ParseAppName(bucketName string) string {
	app, _ := ParseAppEnv(bucketName)
	return app
}

// ParseAppEnv extracts app name and env from a bucket name like nimbus--<account>--<app>--<env>.
func ParseAppEnv(bucketName string) (appName, envName string) {
	if !strings.HasPrefix(bucketName, BucketPrefix) {
		return "", ""
	}
	rest := strings.TrimPrefix(bucketName, BucketPrefix)
	// rest = "<account>--<app>--<env>"
	parts := strings.SplitN(rest, "--", 3)
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return "", ""
	}
	return parts[1], parts[2]
}

// List returns all nimbus applications in the given region, grouped by app name.
func (c *Client) List(ctx context.Context, region string) ([]App, error) {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	out, err := svc.GetBuckets(ctx, &lightsail.GetBucketsInput{})
	if err != nil {
		return nil, err
	}
	appMap := map[string]*App{}
	for _, b := range out.Buckets {
		if b.Name == nil || !strings.HasPrefix(*b.Name, BucketPrefix) {
			continue
		}
		name, env := ParseAppEnv(*b.Name)
		if name == "" || env == "" {
			continue
		}
		state := "active"
		if b.State != nil && b.State.Code != nil {
			state = *b.State.Code
		}
		r := region
		if b.Location != nil {
			r = string(b.Location.RegionName)
		}
		if a, ok := appMap[name]; ok {
			a.Envs = append(a.Envs, env)
			if state != "active" {
				a.State = state
			}
		} else {
			appMap[name] = &App{Name: name, Bucket: *b.Name, Region: r, State: state, Envs: []string{env}}
		}
	}
	var apps []App
	for _, a := range appMap {
		apps = append(apps, *a)
	}
	return apps, nil
}

// GetDetail fetches full application detail including environments, targets, and status.
func (c *Client) GetDetail(ctx context.Context, appName, bucketName, region string) (*Detail, error) {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())

	// Find all env buckets for this app
	allBuckets, err := svc.GetBuckets(ctx, &lightsail.GetBucketsInput{})
	if err != nil {
		return nil, err
	}
	envBuckets := map[string]string{} // env -> bucket name
	state := "active"
	for _, b := range allBuckets.Buckets {
		if b.Name == nil || !strings.HasPrefix(*b.Name, BucketPrefix) {
			continue
		}
		bApp, bEnv := ParseAppEnv(*b.Name)
		if bApp != appName {
			continue
		}
		envBuckets[bEnv] = *b.Name
		if b.State != nil && b.State.Code != nil && *b.State.Code != "active" {
			state = *b.State.Code
		}
	}

	// Build environments from instance tags
	envMap := map[string]*Environment{}
	prefix := TagPrefix + appName + ":"
	c.forEachInstance(ctx, svc, func(inst lstypes.Instance) {
		for _, tag := range inst.Tags {
			if tag.Key == nil || !strings.HasPrefix(*tag.Key, prefix) {
				continue
			}
			envName := strings.TrimPrefix(*tag.Key, prefix)
			if envName == "" {
				continue
			}
			env, ok := envMap[envName]
			if !ok {
				env = &Environment{Name: envName, Bucket: envBuckets[envName]}
				envMap[envName] = env
			}
			env.Targets = append(env.Targets, instanceToTarget(inst, region))
		}
	})

	// Ensure envs with buckets but no targets still appear
	for envName, bkt := range envBuckets {
		if _, ok := envMap[envName]; !ok {
			envMap[envName] = &Environment{Name: envName, Bucket: bkt}
		}
	}

	// Fetch status files from each env bucket
	s3svc := s3.NewFromConfig(c.aws.WithRegion(region).Config())
	for _, env := range envMap {
		if env.Bucket == "" {
			continue
		}
		c.fetchEnvStatuses(ctx, s3svc, env)
	}

	var envs []Environment
	for _, e := range envMap {
		envs = append(envs, *e)
	}

	displayBucket := bucketName
	if displayBucket == "" && len(envBuckets) > 0 {
		for _, b := range envBuckets {
			displayBucket = b
			break
		}
	}

	return &Detail{
		App:          App{Name: appName, Bucket: displayBucket, Region: region, State: state},
		Environments: envs,
	}, nil
}

// fetchEnvStatuses downloads *_status.json files from a bucket and attaches to matching targets.
// FetchBucketStatuses reads all *_status.json files from a bucket and returns them.
func (c *Client) FetchBucketStatuses(ctx context.Context, bucketName, region string) ([]InstanceStatus, error) {
	s3svc := s3.NewFromConfig(c.aws.WithRegion(region).Config())
	listOut, err := s3svc.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &bucketName,
	})
	if err != nil {
		return nil, err
	}
	var statuses []InstanceStatus
	for _, obj := range listOut.Contents {
		if obj.Key == nil || !strings.HasSuffix(*obj.Key, "_status.json") {
			continue
		}
		getOut, err := s3svc.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucketName, Key: obj.Key})
		if err != nil {
			continue
		}
		data, err := io.ReadAll(getOut.Body)
		getOut.Body.Close()
		if err != nil {
			continue
		}
		var status InstanceStatus
		if json.Unmarshal(data, &status) == nil {
			statuses = append(statuses, status)
		}
	}
	return statuses, nil
}

func (c *Client) fetchEnvStatuses(ctx context.Context, s3svc *s3.Client, env *Environment) {
	listOut, err := s3svc.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &env.Bucket,
		Prefix: strPtr(""),
	})
	if err != nil {
		return
	}
	for _, obj := range listOut.Contents {
		if obj.Key == nil || !strings.HasSuffix(*obj.Key, "_status.json") {
			continue
		}
		getOut, err := s3svc.GetObject(ctx, &s3.GetObjectInput{
			Bucket: &env.Bucket,
			Key:    obj.Key,
		})
		if err != nil {
			continue
		}
		data, err := io.ReadAll(getOut.Body)
		getOut.Body.Close()
		if err != nil {
			continue
		}
		var status InstanceStatus
		if json.Unmarshal(data, &status) != nil {
			continue
		}
		// Attach to matching target
		for i := range env.Targets {
			if env.Targets[i].Name == status.Instance {
				env.Targets[i].InstanceStatus = &status
			}
		}
	}
}

// Create creates a new application bucket for the given env.
func (c *Client) Create(ctx context.Context, accountID, appName, envName, region string) error {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	bucketName := BucketName(accountID, appName, envName)
	bundleID := "small_1_0"
	_, err := svc.CreateBucket(ctx, &lightsail.CreateBucketInput{
		BucketName: &bucketName,
		BundleId:   &bundleID,
	})
	return err
}

// Delete performs the full application deletion workflow:
// 1. Stop deployments on all target instances (local down)
// 2. Remove instance tags
// 3. Clean up firewall rules (only if no other app needs them)
// 4. Delete environment buckets
func (c *Client) Delete(ctx context.Context, appName, region string) error {
	c.CleanupInstances(ctx, appName, region) // best-effort
	if err := c.DeleteTags(ctx, appName, region); err != nil {
		return err
	}
	c.CleanupFirewall(ctx, appName, region) // best-effort
	return c.DeleteBuckets(ctx, appName, region)
}

// CleanupInstances SSHes to each tagged instance for the app and runs 'local down'.
func (c *Client) CleanupInstances(ctx context.Context, appName, region string) error {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	prefix := TagPrefix + appName + ":"
	c.forEachInstance(ctx, svc, func(inst lstypes.Instance) {
		for _, tag := range inst.Tags {
			if tag.Key == nil || !strings.HasPrefix(*tag.Key, prefix) {
				continue
			}
			envName := strings.TrimPrefix(*tag.Key, prefix)
			if envName == "" || inst.Name == nil {
				continue
			}
			c.RemoteDown(ctx, *inst.Name, appName, envName, region) // best-effort
		}
	})
	return nil
}

// DeleteTags removes all instance tags for the app.
func (c *Client) DeleteTags(ctx context.Context, appName, region string) error {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	prefix := TagPrefix + appName + ":"
	c.forEachInstance(ctx, svc, func(inst lstypes.Instance) {
		var keys []string
		for _, tag := range inst.Tags {
			if tag.Key != nil && strings.HasPrefix(*tag.Key, prefix) {
				keys = append(keys, *tag.Key)
			}
		}
		if len(keys) > 0 {
			svc.UntagResource(ctx, &lightsail.UntagResourceInput{
				ResourceName: inst.Name,
				TagKeys:      keys,
			})
		}
	})
	return nil
}

// DeleteBuckets deletes all env buckets for the app.
func (c *Client) DeleteBuckets(ctx context.Context, appName, region string) error {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	allBuckets, err := svc.GetBuckets(ctx, &lightsail.GetBucketsInput{})
	if err != nil {
		return err
	}
	forceDelete := true
	for _, b := range allBuckets.Buckets {
		if b.Name == nil {
			continue
		}
		bApp, _ := ParseAppEnv(*b.Name)
		if bApp != appName {
			continue
		}
		svc.DeleteBucket(ctx, &lightsail.DeleteBucketInput{
			BucketName:  b.Name,
			ForceDelete: &forceDelete,
		})
	}
	return nil
}

// AddTarget tags an instance as a deployment target for an app/env, creates a
// bucket access key, writes credentials, uploads the binary, and installs the watch service.
func (c *Client) AddTarget(ctx context.Context, instanceName, appName, envName, accountID, region string) error {
	if err := c.TagTarget(ctx, instanceName, appName, envName, region); err != nil {
		return err
	}
	keyID, secret, err := c.CreateTargetKey(ctx, appName, envName, accountID, region)
	if err != nil {
		return err
	}
	if err := c.WriteTargetCredentials(ctx, instanceName, appName, envName, keyID, secret, BucketName(accountID, appName, envName), region); err != nil {
		return err
	}
	if err := c.UploadBinary(ctx, instanceName, region); err != nil {
		return err
	}
	return c.RemoteUp(ctx, instanceName, appName, envName, region)
}

// TagTarget tags an instance as a deployment target for an app/env.
func (c *Client) TagTarget(ctx context.Context, instanceName, appName, envName, region string) error {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	tagKey := fmt.Sprintf("%s%s:%s", TagPrefix, appName, envName)
	tagVal := "true"
	_, err := svc.TagResource(ctx, &lightsail.TagResourceInput{
		ResourceName: &instanceName,
		Tags:         []lstypes.Tag{{Key: &tagKey, Value: &tagVal}},
	})
	return err
}

// CreateTargetKey creates a bucket access key for the target instance.
func (c *Client) CreateTargetKey(ctx context.Context, appName, envName, accountID, region string) (keyID, secret string, err error) {
	bucketName := BucketName(accountID, appName, envName)
	return c.CreateBucketAccessKey(ctx, bucketName, region)
}

// WriteTargetCredentials SSHes to the instance and writes credentials files.
func (c *Client) WriteTargetCredentials(ctx context.Context, instanceName, appName, envName, keyID, secret, bucketName, region string) error {
	creds, err := c.GetSSHCredentials(ctx, instanceName, region)
	if err != nil {
		return fmt.Errorf("get SSH credentials: %w", err)
	}
	defer os.Remove(creds.KeyPath)
	defer os.Remove(creds.KeyPath + "-cert.pub")

	remoteDir := fmt.Sprintf("/opt/nimbus/%s/%s", appName, envName)
	credContent := fmt.Sprintf("AWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\nAWS_DEFAULT_REGION=%s\n", keyID, secret, region)
	remoteCmd := fmt.Sprintf(
		"sudo mkdir -p %s && echo '%s' | sudo tee %s/.credentials > /dev/null && sudo chmod 600 %s/.credentials && echo '%s' | sudo tee %s/.bucket > /dev/null && echo '%s' | sudo tee %s/.instance > /dev/null",
		remoteDir, credContent, remoteDir, remoteDir, bucketName, remoteDir, instanceName, remoteDir,
	)

	sshTarget := fmt.Sprintf("%s@%s", creds.Username, creds.IP)
	sshOpts := sshOptions(creds.KeyPath)
	cmd := exec.CommandContext(ctx, "ssh", append(sshOpts, sshTarget, remoteCmd)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("write credentials to instance: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// UploadBinary SCPs the nimbus binary from dist/ to the instance, if it exists.
func (c *Client) UploadBinary(ctx context.Context, instanceName, region string) error {
	if _, err := os.Stat("dist/nimbus"); err != nil {
		return nil // no binary to upload, skip
	}
	creds, err := c.GetSSHCredentials(ctx, instanceName, region)
	if err != nil {
		return fmt.Errorf("get SSH credentials: %w", err)
	}
	defer os.Remove(creds.KeyPath)
	defer os.Remove(creds.KeyPath + "-cert.pub")

	sshTarget := fmt.Sprintf("%s@%s", creds.Username, creds.IP)
	sshOpts := sshOptions(creds.KeyPath)
	scpCmd := exec.CommandContext(ctx, "scp", append(sshOpts, "dist/nimbus", sshTarget+":/tmp/nimbus")...)
	if output, err := scpCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp nimbus binary: %s: %w", strings.TrimSpace(string(output)), err)
	}
	mvCmd := exec.CommandContext(ctx, "ssh", append(sshOpts, sshTarget, "sudo mv /tmp/nimbus /usr/local/bin/nimbus && sudo chmod +x /usr/local/bin/nimbus")...)
	if output, err := mvCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("install nimbus binary: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// sshOptions returns common SSH/SCP options with timeouts.
func sshOptions(keyPath string) []string {
	return []string{"-i", keyPath, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
}

// sshTimeout returns a context with a 5-minute timeout for SSH operations.
func sshTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 5*time.Minute)
}

// RemoteUp SSHes to the instance and runs nimbus app local up.
func (c *Client) RemoteUp(ctx context.Context, instanceName, appName, envName, region string) error {
	ctx, cancel := sshTimeout(ctx)
	defer cancel()

	creds, err := c.GetSSHCredentials(ctx, instanceName, region)
	if err != nil {
		return fmt.Errorf("get SSH credentials: %w", err)
	}
	defer os.Remove(creds.KeyPath)
	defer os.Remove(creds.KeyPath + "-cert.pub")

	remoteCmd := fmt.Sprintf("sudo /usr/local/bin/nimbus app local up --name %s --env %s", appName, envName)
	sshTarget := fmt.Sprintf("%s@%s", creds.Username, creds.IP)
	cmd := exec.CommandContext(ctx, "ssh", append(sshOptions(creds.KeyPath), sshTarget, remoteCmd)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("remote up timed out after 5m: %w", ctx.Err())
		}
		return fmt.Errorf("remote up: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// RemoteDown SSHes to the instance and runs nimbus app local down.
func (c *Client) RemoteDown(ctx context.Context, instanceName, appName, envName, region string) error {
	ctx, cancel := sshTimeout(ctx)
	defer cancel()

	creds, err := c.GetSSHCredentials(ctx, instanceName, region)
	if err != nil {
		return fmt.Errorf("get SSH credentials: %w", err)
	}
	defer os.Remove(creds.KeyPath)
	defer os.Remove(creds.KeyPath + "-cert.pub")

	remoteCmd := fmt.Sprintf("sudo /usr/local/bin/nimbus app local down --name %s --env %s", appName, envName)
	sshTarget := fmt.Sprintf("%s@%s", creds.Username, creds.IP)
	cmd := exec.CommandContext(ctx, "ssh", append(sshOptions(creds.KeyPath), sshTarget, remoteCmd)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("remote down timed out after 5m: %w", ctx.Err())
		}
		return fmt.Errorf("remote down: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// RemoveTarget removes an instance's association tag for an app/env.
// If cleanup is true, it SSHes to the instance and runs 'nimbus app local down'.
func (c *Client) RemoveTarget(ctx context.Context, instanceName, appName, envName, region string, cleanup bool) error {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	tagKey := fmt.Sprintf("%s%s:%s", TagPrefix, appName, envName)
	_, err := svc.UntagResource(ctx, &lightsail.UntagResourceInput{
		ResourceName: &instanceName,
		TagKeys:      []string{tagKey},
	})
	if err != nil {
		return err
	}

	if !cleanup {
		return nil
	}

	c.RemoteDown(ctx, instanceName, appName, envName, region) // best-effort: compose down + uninstall watch + remove files
	return nil
}

// FindTarget finds the first instance tagged for the given app/env and returns it.
func (c *Client) FindTarget(ctx context.Context, appName, envName, region string) (*Target, error) {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	tagKey := fmt.Sprintf("%s%s:%s", TagPrefix, appName, envName)

	var found *Target
	c.forEachInstance(ctx, svc, func(inst lstypes.Instance) {
		if found != nil {
			return
		}
		for _, tag := range inst.Tags {
			if tag.Key != nil && *tag.Key == tagKey {
				t := instanceToTarget(inst, region)
				if t.IP != "" {
					found = &t
				}
				return
			}
		}
	})
	if found == nil {
		return nil, fmt.Errorf("no instance found with tag %s", tagKey)
	}
	return found, nil
}

// CreateBucketAccessKey creates a short-lived access key for external bucket access.
func (c *Client) CreateBucketAccessKey(ctx context.Context, bucketName, region string) (accessKeyID, secretKey string, err error) {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	out, err := svc.CreateBucketAccessKey(ctx, &lightsail.CreateBucketAccessKeyInput{
		BucketName: &bucketName,
	})
	if err != nil {
		return "", "", fmt.Errorf("create bucket access key: %w", err)
	}
	return *out.AccessKey.AccessKeyId, *out.AccessKey.SecretAccessKey, nil
}

// DeleteBucketAccessKey removes a bucket access key.
func (c *Client) DeleteBucketAccessKey(ctx context.Context, bucketName, accessKeyID, region string) error {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	_, err := svc.DeleteBucketAccessKey(ctx, &lightsail.DeleteBucketAccessKeyInput{
		BucketName:  &bucketName,
		AccessKeyId: &accessKeyID,
	})
	return err
}

// GetSSHCredentials fetches temporary SSH credentials for an instance.
func (c *Client) GetSSHCredentials(ctx context.Context, instanceName, region string) (*SSHCredentials, error) {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
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

	return &SSHCredentials{KeyPath: keyFile.Name(), Username: *d.Username, IP: *d.IpAddress}, nil
}

// ListInstances returns all instances in the region (for target selection).
func (c *Client) ListInstances(ctx context.Context, region string) ([]Target, error) {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	var targets []Target
	c.forEachInstance(ctx, svc, func(inst lstypes.Instance) {
		targets = append(targets, instanceToTarget(inst, region))
	})
	return targets, nil
}

// InstallWatchService writes and enables a systemd unit for the watch loop.
// It reads credentials from /opt/nimbus/<app>/<env>/.credentials (written by AddTarget).
func InstallWatchService(appName, envName string) error {
	credsPath := fmt.Sprintf("/opt/nimbus/%s/%s/.credentials", appName, envName)
	if _, err := os.Stat(credsPath); err != nil {
		return fmt.Errorf("credentials file not found at %s — run add-target first: %w", credsPath, err)
	}

	unit := fmt.Sprintf(`[Unit]
Description=Nimbus deployment watcher for %s/%s
After=network.target

[Service]
Type=simple
EnvironmentFile=/opt/nimbus/%s/%s/.credentials
ExecStart=/usr/local/bin/nimbus application local watch --name %s --env %s
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
`, appName, envName, appName, envName, appName, envName)

	svcName := fmt.Sprintf("nimbus-watch-%s-%s", appName, envName)
	path := fmt.Sprintf("/etc/systemd/system/%s.service", svcName)
	if err := os.WriteFile(path, []byte(unit), 0644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		return err
	}
	if err := runSystemctl("enable", svcName); err != nil {
		return err
	}
	return runSystemctl("start", svcName)
}

func runSystemctl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// UninstallWatchService stops and removes the systemd watch service for an app/env.
// Intended to run on the instance itself.
func UninstallWatchService(appName, envName string) error {
	svcName := fmt.Sprintf("nimbus-watch-%s-%s", appName, envName)
	path := fmt.Sprintf("/etc/systemd/system/%s.service", svcName)
	// Best-effort stop and disable
	runSystemctl("stop", svcName)
	runSystemctl("disable", svcName)
	os.Remove(path)
	return runSystemctl("daemon-reload")
}

// forEachInstance iterates over all instances, handling pagination.
func (c *Client) forEachInstance(ctx context.Context, svc *lightsail.Client, fn func(lstypes.Instance)) {
	var pageToken *string
	for {
		out, err := svc.GetInstances(ctx, &lightsail.GetInstancesInput{PageToken: pageToken})
		if err != nil {
			break
		}
		for _, inst := range out.Instances {
			fn(inst)
		}
		if out.NextPageToken == nil {
			break
		}
		pageToken = out.NextPageToken
	}
}

func instanceToTarget(inst lstypes.Instance, region string) Target {
	ip := ""
	if inst.PublicIpAddress != nil {
		ip = *inst.PublicIpAddress
	}
	state := ""
	if inst.State != nil && inst.State.Name != nil {
		state = *inst.State.Name
	}
	return Target{Name: *inst.Name, State: state, IP: ip, Region: region}
}

func strPtr(s string) *string { return &s }

// OpenFirewallPorts ensures the given TCP ports are open on the instance's Lightsail firewall.
func (c *Client) OpenFirewallPorts(ctx context.Context, instanceName, region string, ports []int) error {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())

	inst, err := svc.GetInstance(ctx, &lightsail.GetInstanceInput{InstanceName: &instanceName})
	if err != nil {
		return fmt.Errorf("get instance: %w", err)
	}

	existing := inst.Instance.Networking.Ports
	have := map[int32]bool{}
	var rules []lstypes.PortInfo
	for _, p := range existing {
		rules = append(rules, lstypes.PortInfo{
			FromPort: p.FromPort,
			ToPort:   p.ToPort,
			Protocol: p.Protocol,
			Cidrs:    p.Cidrs,
		})
		if p.Protocol == lstypes.NetworkProtocolTcp && p.FromPort == p.ToPort {
			have[p.FromPort] = true
		}
	}

	added := 0
	for _, port := range ports {
		p := int32(port)
		if have[p] {
			continue
		}
		rules = append(rules, lstypes.PortInfo{
			FromPort: p,
			ToPort:   p,
			Protocol: lstypes.NetworkProtocolTcp,
			Cidrs:    []string{"0.0.0.0/0"},
		})
		added++
	}

	if added == 0 {
		return nil
	}

	_, err = svc.PutInstancePublicPorts(ctx, &lightsail.PutInstancePublicPortsInput{
		InstanceName: &instanceName,
		PortInfos:    rules,
	})
	return err
}

// CleanupFirewall removes non-default firewall rules from instances that were
// targets of the given app, but only if no other nimbus app is tagged on them.
func (c *Client) CleanupFirewall(ctx context.Context, appName, region string) error {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	c.forEachInstance(ctx, svc, func(inst lstypes.Instance) {
		if inst.Name == nil {
			return
		}
		// Check if this instance was a target for the app being deleted
		wasTarget := false
		otherApps := false
		for _, tag := range inst.Tags {
			if tag.Key == nil || !strings.HasPrefix(*tag.Key, TagPrefix) {
				continue
			}
			tagApp := strings.SplitN(strings.TrimPrefix(*tag.Key, TagPrefix), ":", 2)[0]
			if tagApp == appName {
				wasTarget = true
			} else {
				otherApps = true
			}
		}
		if !wasTarget || otherApps {
			return
		}
		// No other nimbus apps — reset to default ports (SSH only)
		svc.PutInstancePublicPorts(ctx, &lightsail.PutInstancePublicPortsInput{
			InstanceName: inst.Name,
			PortInfos: []lstypes.PortInfo{
				{FromPort: 22, ToPort: 22, Protocol: lstypes.NetworkProtocolTcp, Cidrs: []string{"0.0.0.0/0"}},
			},
		})
	})
	return nil
}
