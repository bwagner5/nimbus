package applications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	lstypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const envOrderKey = "config/environments.json"

type envOrderDoc struct {
	Environments []string `json:"environments"`
}

// appConfigBucket resolves the app config bucket name.
func (c *Client) appConfigBucket(ctx context.Context, appName string) (string, error) {
	accountID, err := c.AccountID(ctx)
	if err != nil {
		return "", err
	}
	return AppBucketName(accountID, appName), nil
}

// s3WithKey creates a temporary access key for a bucket, returns an S3 client and cleanup func.
func (c *Client) s3WithKey(ctx context.Context, bucketName, region string) (*s3.Client, func(), error) {
	keyID, secret, err := c.CreateBucketAccessKey(ctx, bucketName, region)
	if err != nil {
		return nil, nil, fmt.Errorf("access key for %s: %w", bucketName, err)
	}
	cleanup := func() { c.DeleteBucketAccessKey(ctx, bucketName, keyID, region) }
	svc := s3.New(s3.Options{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(keyID, secret, ""),
	})
	return svc, cleanup, nil
}

// putWithRetry writes to S3 with back-off retries for eventual consistency.
func putWithRetry(ctx context.Context, svc *s3.Client, bucket, key string, data []byte) error {
	ct := "application/json"
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		_, err = svc.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      &bucket,
			Key:         &key,
			Body:        bytes.NewReader(data),
			ContentType: &ct,
		})
		if err == nil {
			return nil
		}
	}
	return err
}

// GetEnvOrder reads the environment ordering from the app config bucket.
// Reconciles stored order with actual env buckets.
func (c *Client) GetEnvOrder(ctx context.Context, appName, region string) ([]string, error) {
	bucketMap, err := c.appBucketMap(ctx, appName, region)
	if err != nil {
		return nil, err
	}
	if len(bucketMap) == 0 {
		return nil, nil
	}

	stored := c.readEnvOrder(ctx, appName, region)

	// Reconcile: keep stored entries that still have buckets, append new ones sorted
	var order []string
	seen := map[string]bool{}
	for _, e := range stored {
		if _, ok := bucketMap[e]; ok && !seen[e] {
			order = append(order, e)
			seen[e] = true
		}
	}
	var remaining []string
	for e := range bucketMap {
		if !seen[e] {
			remaining = append(remaining, e)
		}
	}
	if len(remaining) > 0 {
		// sort inline to avoid import
		for i := 0; i < len(remaining); i++ {
			for j := i + 1; j < len(remaining); j++ {
				if remaining[j] < remaining[i] {
					remaining[i], remaining[j] = remaining[j], remaining[i]
				}
			}
		}
		order = append(order, remaining...)
	}
	return order, nil
}

// appBucketMap returns env -> bucket name for all env buckets belonging to appName.
func (c *Client) appBucketMap(ctx context.Context, appName, region string) (map[string]string, error) {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	allBuckets, err := svc.GetBuckets(ctx, &lightsail.GetBucketsInput{})
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, b := range allBuckets.Buckets {
		if b.Name == nil {
			continue
		}
		bApp, bEnv := ParseAppEnv(*b.Name)
		if bApp == appName && bEnv != "" {
			m[bEnv] = *b.Name
		}
	}
	return m, nil
}

// readEnvOrder reads the config from the app config bucket using default credentials.
func (c *Client) readEnvOrder(ctx context.Context, appName, region string) []string {
	cfgBucket, err := c.appConfigBucket(ctx, appName)
	if err != nil {
		return nil
	}
	s3svc := s3.NewFromConfig(c.aws.WithRegion(region).Config())
	key := envOrderKey
	out, err := s3svc.GetObject(ctx, &s3.GetObjectInput{Bucket: &cfgBucket, Key: &key})
	if err != nil {
		return nil
	}
	data, _ := io.ReadAll(out.Body)
	out.Body.Close()
	var doc envOrderDoc
	if json.Unmarshal(data, &doc) == nil {
		return doc.Environments
	}
	return nil
}

// SetEnvOrder writes the environment ordering to the app config bucket.
func (c *Client) SetEnvOrder(ctx context.Context, appName, region string, order []string) error {
	if len(order) == 0 {
		return fmt.Errorf("order must not be empty")
	}
	cfgBucket, err := c.appConfigBucket(ctx, appName)
	if err != nil {
		return err
	}

	svc, cleanup, err := c.s3WithKey(ctx, cfgBucket, region)
	if err != nil {
		return err
	}
	defer cleanup()

	data, _ := json.Marshal(envOrderDoc{Environments: order})
	return putWithRetry(ctx, svc, cfgBucket, envOrderKey, data)
}

// AddEnvironment creates a new env bucket and appends it to the env order.
func (c *Client) AddEnvironment(ctx context.Context, appName, envName, region string) error {
	accountID, err := c.AccountID(ctx)
	if err != nil {
		return err
	}

	// Get current order BEFORE creating the new bucket
	order, _ := c.GetEnvOrder(ctx, appName, region)

	if err := c.Create(ctx, accountID, appName, envName, region); err != nil {
		return fmt.Errorf("create bucket: %w", err)
	}

	// Append new env
	found := false
	for _, e := range order {
		if e == envName {
			found = true
			break
		}
	}
	if !found {
		order = append(order, envName)
	}

	// Write to the app config bucket (which already exists from Create)
	return c.SetEnvOrder(ctx, appName, region, order)
}

// DeleteEnvironment removes an environment: disassociates all targets, deletes the bucket, and updates env order.
func (c *Client) DeleteEnvironment(ctx context.Context, appName, envName, region string) error {
	accountID, err := c.AccountID(ctx)
	if err != nil {
		return err
	}

	// Disassociate all targets for this env
	c.DisassociateEnvTargets(ctx, appName, envName, region)

	// Delete the env bucket
	bucketName := BucketName(accountID, appName, envName)
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	forceDelete := true
	svc.DeleteBucket(ctx, &lightsail.DeleteBucketInput{
		BucketName:  &bucketName,
		ForceDelete: &forceDelete,
	})

	// Update env order
	order, _ := c.GetEnvOrder(ctx, appName, region)
	var newOrder []string
	for _, e := range order {
		if e != envName {
			newOrder = append(newOrder, e)
		}
	}
	if len(newOrder) > 0 {
		return c.SetEnvOrder(ctx, appName, region, newOrder)
	}
	return nil
}

// DisassociateEnvTargets removes all targets associated with an env.
func (c *Client) DisassociateEnvTargets(ctx context.Context, appName, envName, region string) {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	tagKey := fmt.Sprintf("%s%s:%s", TagPrefix, appName, envName)
	c.forEachInstance(ctx, svc, func(inst lstypes.Instance) {
		for _, tag := range inst.Tags {
			if tag.Key != nil && *tag.Key == tagKey && inst.Name != nil {
				c.RemoveTarget(ctx, *inst.Name, appName, envName, region, true)
			}
		}
	})
}

// DeleteEnvBucket deletes the bucket for an environment.
func (c *Client) DeleteEnvBucket(ctx context.Context, appName, envName, region string) error {
	accountID, err := c.AccountID(ctx)
	if err != nil {
		return err
	}
	bucketName := BucketName(accountID, appName, envName)
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	forceDelete := true
	_, err = svc.DeleteBucket(ctx, &lightsail.DeleteBucketInput{
		BucketName:  &bucketName,
		ForceDelete: &forceDelete,
	})
	return err
}

// SaveFirewallRules persists firewall port rules to the env bucket.
func (c *Client) SaveFirewallRules(ctx context.Context, appName, envName, region string, ports []int) error {
	accountID, err := c.AccountID(ctx)
	if err != nil {
		return err
	}
	bucket := BucketName(accountID, appName, envName)
	svc, cleanup, err := c.s3WithKey(ctx, bucket, region)
	if err != nil {
		return err
	}
	defer cleanup()
	data, _ := json.Marshal(ports)
	return putWithRetry(ctx, svc, bucket, "config/ports.json", data)
}

// LoadFirewallRules reads firewall port rules from the env bucket.
func (c *Client) LoadFirewallRules(ctx context.Context, appName, envName, region string) ([]int, error) {
	accountID, err := c.AccountID(ctx)
	if err != nil {
		return nil, err
	}
	bucket := BucketName(accountID, appName, envName)
	svc, cleanup, err := c.s3WithKey(ctx, bucket, region)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	key := "config/ports.json"
	out, err := svc.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	var ports []int
	if err := json.NewDecoder(out.Body).Decode(&ports); err != nil {
		return nil, err
	}
	return ports, nil
}
