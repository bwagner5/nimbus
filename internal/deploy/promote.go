package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

// PromoteResult holds the intermediate state of a promote operation.
type PromoteResult struct {
	SrcBucket  string
	DestBucket string
	LatestKey  string
	Body       []byte
	NewKey     string
	Ports      []int // firewall ports from source env
}

// PromoteDownload finds and downloads the latest deploy from srcEnv.
func PromoteDownload(ctx context.Context, client *aws.Client, appName, srcEnv, region string) (*PromoteResult, error) {
	appClient := applications.NewClient(client)
	accountID, err := appClient.AccountID(ctx)
	if err != nil {
		return nil, err
	}
	srcBucket := applications.BucketName(accountID, appName, srcEnv)

	srcKeyID, srcSecret, err := appClient.CreateBucketAccessKey(ctx, srcBucket, region)
	if err != nil {
		return nil, fmt.Errorf("source access key: %w", err)
	}
	defer appClient.DeleteBucketAccessKey(ctx, srcBucket, srcKeyID, region)

	srcS3 := s3.New(s3.Options{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(srcKeyID, srcSecret, ""),
	})

	// List with retry for access key propagation
	prefix := "deploy/"
	var listErr error
	var out *s3.ListObjectsV2Output
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		out, listErr = srcS3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: &srcBucket,
			Prefix: &prefix,
		})
		if listErr == nil {
			break
		}
	}
	if listErr != nil {
		return nil, fmt.Errorf("list source deploys: %w", listErr)
	}
	if len(out.Contents) == 0 {
		return nil, fmt.Errorf("no deploys found in %s/%s", appName, srcEnv)
	}
	sort.Slice(out.Contents, func(i, j int) bool {
		return *out.Contents[i].Key > *out.Contents[j].Key
	})
	latestKey := *out.Contents[0].Key

	getOut, err := srcS3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &srcBucket,
		Key:    &latestKey,
	})
	if err != nil {
		return nil, fmt.Errorf("download from source: %w", err)
	}
	body, err := io.ReadAll(getOut.Body)
	getOut.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read source object: %w", err)
	}

	suffix := "promote"
	base := strings.TrimPrefix(latestKey, "deploy/")
	if idx := strings.Index(base, "-"); idx > 0 {
		suffix = strings.TrimSuffix(base[idx+1:], ".tar.gz") + "-promote"
	}
	newKey := fmt.Sprintf("deploy/%d-%s.tar.gz", time.Now().Unix(), suffix)

	// Load firewall rules from source env (best-effort)
	ports, _ := appClient.LoadFirewallRules(ctx, appName, srcEnv, region)

	return &PromoteResult{
		SrcBucket: srcBucket,
		LatestKey: latestKey,
		Body:      body,
		NewKey:    newKey,
		Ports:     ports,
	}, nil
}

// PromoteUpload uploads the downloaded deploy to destEnv.
func PromoteUpload(ctx context.Context, client *aws.Client, appName, destEnv, region string, r *PromoteResult) error {
	appClient := applications.NewClient(client)
	accountID, err := appClient.AccountID(ctx)
	if err != nil {
		return err
	}
	destBucket := applications.BucketName(accountID, appName, destEnv)

	destKeyID, destSecret, err := appClient.CreateBucketAccessKey(ctx, destBucket, region)
	if err != nil {
		return fmt.Errorf("dest access key: %w", err)
	}
	defer appClient.DeleteBucketAccessKey(ctx, destBucket, destKeyID, region)

	destS3 := s3.New(s3.Options{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(destKeyID, destSecret, ""),
	})

	// Upload with retry for access key propagation
	size := int64(len(r.Body))
	var uploadErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		_, uploadErr = destS3.PutObject(ctx, &s3.PutObjectInput{
			Bucket:        &destBucket,
			Key:           &r.NewKey,
			Body:          bytes.NewReader(r.Body),
			ContentLength: awssdk.Int64(size),
		})
		if uploadErr == nil {
			break
		}
	}
	if uploadErr != nil {
		return fmt.Errorf("upload to dest: %w", uploadErr)
	}

	// Apply firewall rules from source env to dest targets
	if len(r.Ports) > 0 {
		appClient.SaveFirewallRules(ctx, appName, destEnv, region, r.Ports)
		if target, err := appClient.FindTarget(ctx, appName, destEnv, region); err == nil {
			appClient.OpenFirewallPorts(ctx, target.Name, region, r.Ports)
		}
	}
	return nil
}

// Promote copies the latest deploy asset from srcEnv to destEnv (CLI version with prints).
func Promote(ctx context.Context, client *aws.Client, appName, srcEnv, destEnv, region string) error {
	fmt.Printf("📥 Downloading latest deploy from %s...\n", srcEnv)
	r, err := PromoteDownload(ctx, client, appName, srcEnv, region)
	if err != nil {
		return err
	}
	fmt.Printf("📤 Uploading %s to %s (%d bytes)...\n", r.NewKey, destEnv, len(r.Body))
	if err := PromoteUpload(ctx, client, appName, destEnv, region, r); err != nil {
		return err
	}
	fmt.Printf("✅ Promoted %s/%s → %s/%s (%s)\n", appName, srcEnv, appName, destEnv, r.NewKey)
	return nil
}
