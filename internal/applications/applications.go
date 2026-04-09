// Package applications provides the Lightsail Application SDK for managing
// nimbus applications (buckets, instance tags, SSH access).
package applications

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	lstypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

const (
	BucketPrefix = "ls-app-"
	TagPrefix    = "nimbus:app:"
)

// App represents a discovered nimbus application.
type App struct {
	Name   string
	Bucket string
	Region string
	State  string
}

// Environment represents an app environment parsed from instance tags.
type Environment struct {
	Name    string
	Targets []Target
}

// Target is a Lightsail instance associated with an app/env.
type Target struct {
	Name   string
	State  string
	IP     string
	Region string
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

// BucketName returns the expected bucket name for an app given an account ID.
func BucketName(accountID, appName string) string {
	return fmt.Sprintf("%s%s-%s", BucketPrefix, accountID, appName)
}

// ParseAppName extracts the app name from a bucket name like ls-app-123456-myapp.
func ParseAppName(bucketName string) string {
	if !strings.HasPrefix(bucketName, BucketPrefix) {
		return ""
	}
	rest := strings.TrimPrefix(bucketName, BucketPrefix)
	parts := strings.SplitN(rest, "-", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// List returns all nimbus applications in the given region.
func (c *Client) List(ctx context.Context, region string) ([]App, error) {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	out, err := svc.GetBuckets(ctx, &lightsail.GetBucketsInput{})
	if err != nil {
		return nil, err
	}
	var apps []App
	for _, b := range out.Buckets {
		if b.Name == nil || !strings.HasPrefix(*b.Name, BucketPrefix) {
			continue
		}
		name := ParseAppName(*b.Name)
		if name == "" {
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
		apps = append(apps, App{Name: name, Bucket: *b.Name, Region: r, State: state})
	}
	return apps, nil
}

// GetDetail fetches full application detail including environments and targets.
func (c *Client) GetDetail(ctx context.Context, appName, bucketName, region string) (*Detail, error) {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())

	buckOut, err := svc.GetBuckets(ctx, &lightsail.GetBucketsInput{BucketName: &bucketName})
	if err != nil {
		return nil, err
	}
	state := "active"
	if len(buckOut.Buckets) > 0 && buckOut.Buckets[0].State != nil && buckOut.Buckets[0].State.Code != nil {
		state = *buckOut.Buckets[0].State.Code
	}

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
				env = &Environment{Name: envName}
				envMap[envName] = env
			}
			env.Targets = append(env.Targets, instanceToTarget(inst, region))
		}
	})

	var envs []Environment
	for _, e := range envMap {
		envs = append(envs, *e)
	}

	return &Detail{
		App:          App{Name: appName, Bucket: bucketName, Region: region, State: state},
		Environments: envs,
	}, nil
}

// Create creates a new application bucket.
func (c *Client) Create(ctx context.Context, accountID, appName, region string) error {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	bucketName := BucketName(accountID, appName)
	bundleID := "small_1_0"
	_, err := svc.CreateBucket(ctx, &lightsail.CreateBucketInput{
		BucketName: &bucketName,
		BundleId:   &bundleID,
	})
	return err
}

// Delete removes all instance tags for the app and deletes the bucket.
func (c *Client) Delete(ctx context.Context, appName, region string) error {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	prefix := TagPrefix + appName + ":"

	// Remove tags from instances
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

	// Resolve and delete bucket
	accountID, err := c.AccountID(ctx)
	if err != nil {
		return err
	}
	bucketName := BucketName(accountID, appName)
	forceDelete := true
	_, err = svc.DeleteBucket(ctx, &lightsail.DeleteBucketInput{
		BucketName:  &bucketName,
		ForceDelete: &forceDelete,
	})
	return err
}

// AddTarget tags an instance as a deployment target for an app/env.
func (c *Client) AddTarget(ctx context.Context, instanceName, appName, envName, region string) error {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	tagKey := fmt.Sprintf("%s%s:%s", TagPrefix, appName, envName)
	tagVal := "true"
	_, err := svc.TagResource(ctx, &lightsail.TagResourceInput{
		ResourceName: &instanceName,
		Tags:         []lstypes.Tag{{Key: &tagKey, Value: &tagVal}},
	})
	return err
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
