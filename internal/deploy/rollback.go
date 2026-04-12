package deploy

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

// Rollback copies the previous deploy asset to a new key with the current timestamp,
// creating a roll-forward entry in the deploy history.
func Rollback(ctx context.Context, client *aws.Client, appName, envName, region string) error {
	appClient := applications.NewClient(client)

	fmt.Println("🔍 Resolving bucket...")
	accountID, err := appClient.AccountID(ctx)
	if err != nil {
		return err
	}
	bucketName := applications.BucketName(accountID, appName, envName)

	fmt.Println("🔑 Creating bucket access key...")
	keyID, secret, err := appClient.CreateBucketAccessKey(ctx, bucketName, region)
	if err != nil {
		return err
	}
	defer func() {
		fmt.Println("🔑 Cleaning up access key...")
		appClient.DeleteBucketAccessKey(ctx, bucketName, keyID, region)
	}()

	s3svc := s3.New(s3.Options{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(keyID, secret, ""),
	})

	fmt.Println("📋 Listing deploy history...")
	prefix := "deploy/"
	out, err := s3svc.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &bucketName,
		Prefix: &prefix,
	})
	if err != nil {
		return fmt.Errorf("list deploys: %w", err)
	}
	if len(out.Contents) < 2 {
		return fmt.Errorf("need at least 2 deploys to rollback, found %d", len(out.Contents))
	}

	sort.Slice(out.Contents, func(i, j int) bool {
		return *out.Contents[i].Key > *out.Contents[j].Key
	})

	previousKey := *out.Contents[1].Key

	// Extract the original suffix (commit hash or "rollback") from the previous key
	// Key format: deploy/<unix>-<suffix>.tar.gz
	suffix := "rollback"
	base := strings.TrimPrefix(previousKey, "deploy/")
	if idx := strings.Index(base, "-"); idx > 0 {
		suffix = strings.TrimSuffix(base[idx+1:], ".tar.gz")
	}

	newKey := fmt.Sprintf("deploy/%d-%s-rollback.tar.gz", time.Now().Unix(), suffix)
	copySource := fmt.Sprintf("%s/%s", bucketName, previousKey)

	fmt.Printf("⏪ Rolling back: %s → %s\n", previousKey, newKey)
	_, err = s3svc.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     &bucketName,
		Key:        &newKey,
		CopySource: &copySource,
	})
	if err != nil {
		return fmt.Errorf("copy object: %w", err)
	}

	fmt.Printf("✅ Rolled back %s/%s to previous deploy\n", appName, envName)
	return nil
}
