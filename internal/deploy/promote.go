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

// Promote copies the latest deploy asset from srcEnv to destEnv.
func Promote(ctx context.Context, client *aws.Client, appName, srcEnv, destEnv, region string) error {
	appClient := applications.NewClient(client)

	accountID, err := appClient.AccountID(ctx)
	if err != nil {
		return err
	}
	srcBucket := applications.BucketName(accountID, appName, srcEnv)
	destBucket := applications.BucketName(accountID, appName, destEnv)

	// Create source key
	fmt.Printf("🔑 Creating access key for %s...\n", srcEnv)
	srcKeyID, srcSecret, err := appClient.CreateBucketAccessKey(ctx, srcBucket, region)
	if err != nil {
		return fmt.Errorf("source access key: %w", err)
	}
	defer func() {
		appClient.DeleteBucketAccessKey(ctx, srcBucket, srcKeyID, region)
	}()

	srcS3 := s3.New(s3.Options{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(srcKeyID, srcSecret, ""),
	})

	// Find latest deploy in source
	fmt.Printf("📋 Finding latest deploy in %s...\n", srcEnv)
	prefix := "deploy/"
	out, err := srcS3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &srcBucket,
		Prefix: &prefix,
	})
	if err != nil {
		return fmt.Errorf("list source deploys: %w", err)
	}
	if len(out.Contents) == 0 {
		return fmt.Errorf("no deploys found in %s/%s", appName, srcEnv)
	}
	sort.Slice(out.Contents, func(i, j int) bool {
		return *out.Contents[i].Key > *out.Contents[j].Key
	})
	latestKey := *out.Contents[0].Key

	// Download from source
	fmt.Printf("📥 Downloading %s...\n", latestKey)
	getOut, err := srcS3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &srcBucket,
		Key:    &latestKey,
	})
	if err != nil {
		return fmt.Errorf("download from source: %w", err)
	}
	body, err := io.ReadAll(getOut.Body)
	getOut.Body.Close()
	if err != nil {
		return fmt.Errorf("read source object: %w", err)
	}

	// Build new key with promote suffix
	suffix := "promote"
	base := strings.TrimPrefix(latestKey, "deploy/")
	if idx := strings.Index(base, "-"); idx > 0 {
		suffix = strings.TrimSuffix(base[idx+1:], ".tar.gz") + "-promote"
	}
	newKey := fmt.Sprintf("deploy/%d-%s.tar.gz", time.Now().Unix(), suffix)

	// Create dest key
	fmt.Printf("🔑 Creating access key for %s...\n", destEnv)
	destKeyID, destSecret, err := appClient.CreateBucketAccessKey(ctx, destBucket, region)
	if err != nil {
		return fmt.Errorf("dest access key: %w", err)
	}
	defer func() {
		appClient.DeleteBucketAccessKey(ctx, destBucket, destKeyID, region)
	}()

	destS3 := s3.New(s3.Options{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(destKeyID, destSecret, ""),
	})

	// Upload to dest
	size := int64(len(body))
	fmt.Printf("📤 Uploading %s to %s (%d bytes)...\n", newKey, destEnv, size)
	_, err = destS3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &destBucket,
		Key:           &newKey,
		Body:          bytes.NewReader(body),
		ContentLength: awssdk.Int64(size),
	})
	if err != nil {
		return fmt.Errorf("upload to dest: %w", err)
	}

	fmt.Printf("✅ Promoted %s/%s → %s/%s (%s)\n", appName, srcEnv, appName, destEnv, newKey)
	return nil
}
