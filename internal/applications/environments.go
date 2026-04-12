package applications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const envOrderKey = "config/environments.json"

type envOrderDoc struct {
	Environments []string `json:"environments"`
}

// GetEnvOrder reads the environment ordering for an app.
// Reconciles stored order with actual buckets: missing removed, new appended.
func (c *Client) GetEnvOrder(ctx context.Context, appName, region string) ([]string, error) {
	svc := lightsail.NewFromConfig(c.aws.WithRegion(region).Config())
	allBuckets, err := svc.GetBuckets(ctx, &lightsail.GetBucketsInput{})
	if err != nil {
		return nil, err
	}

	bucketMap := map[string]string{} // env -> bucket name
	for _, b := range allBuckets.Buckets {
		if b.Name == nil {
			continue
		}
		bApp, bEnv := ParseAppEnv(*b.Name)
		if bApp == appName && bEnv != "" {
			bucketMap[bEnv] = *b.Name
		}
	}
	if len(bucketMap) == 0 {
		return nil, nil
	}

	// Read stored order from any bucket
	s3svc := s3.NewFromConfig(c.aws.WithRegion(region).Config())
	stored := readEnvOrder(ctx, s3svc, bucketMap)

	// Reconcile
	var order []string
	seen := map[string]bool{}
	for _, e := range stored {
		if _, ok := bucketMap[e]; ok && !seen[e] {
			order = append(order, e)
			seen[e] = true
		}
	}
	// Append any envs not in stored order
	var remaining []string
	for e := range bucketMap {
		if !seen[e] {
			remaining = append(remaining, e)
		}
	}
	sort.Strings(remaining)
	order = append(order, remaining...)
	return order, nil
}

func readEnvOrder(ctx context.Context, s3svc *s3.Client, bucketMap map[string]string) []string {
	// Try each bucket sorted by name until we find the config
	var envs []string
	for e := range bucketMap {
		envs = append(envs, e)
	}
	sort.Strings(envs)
	for _, env := range envs {
		bkt := bucketMap[env]
		key := envOrderKey
		out, err := s3svc.GetObject(ctx, &s3.GetObjectInput{Bucket: &bkt, Key: &key})
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(out.Body)
		out.Body.Close()
		var doc envOrderDoc
		if json.Unmarshal(data, &doc) == nil && len(doc.Environments) > 0 {
			return doc.Environments
		}
	}
	return nil
}

// SetEnvOrder writes the environment ordering to the first env's bucket.
func (c *Client) SetEnvOrder(ctx context.Context, appName, region string, order []string) error {
	if len(order) == 0 {
		return fmt.Errorf("order must not be empty")
	}
	accountID, err := c.AccountID(ctx)
	if err != nil {
		return err
	}
	bucketName := BucketName(accountID, appName, order[0])
	data, _ := json.Marshal(envOrderDoc{Environments: order})
	s3svc := s3.NewFromConfig(c.aws.WithRegion(region).Config())
	key := envOrderKey
	ct := "application/json"
	_, err = s3svc.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &bucketName,
		Key:         &key,
		Body:        bytes.NewReader(data),
		ContentType: &ct,
	})
	return err
}

// AddEnvironment creates a new env bucket and appends it to the env order.
func (c *Client) AddEnvironment(ctx context.Context, appName, envName, region string) error {
	accountID, err := c.AccountID(ctx)
	if err != nil {
		return err
	}
	if err := c.Create(ctx, accountID, appName, envName, region); err != nil {
		return fmt.Errorf("create bucket: %w", err)
	}
	order, err := c.GetEnvOrder(ctx, appName, region)
	if err != nil {
		return err
	}
	return c.SetEnvOrder(ctx, appName, region, order)
}
