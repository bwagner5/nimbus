package deploy

import (
	"context"
	"fmt"
	"os"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

// DeployViaBucket uploads a deploy asset to the app-env bucket using short-lived access keys.
func DeployViaBucket(ctx context.Context, client *aws.Client, appName, envName, region string) error {
	if findComposeFile() == "" {
		return fmt.Errorf("no docker-compose.yml or compose.yaml found in current directory")
	}

	appClient := applications.NewClient(client)

	fmt.Println("🔍 Resolving bucket...")
	accountID, err := appClient.AccountID(ctx)
	if err != nil {
		return err
	}
	bucketName := applications.BucketName(accountID, appName, envName)

	commitID := getGitCommit()
	assetName := fmt.Sprintf("deploy/%d-%s.tar.gz", time.Now().Unix(), commitID)

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

	fmt.Println("🔑 Creating bucket access key...")
	keyID, secret, err := appClient.CreateBucketAccessKey(ctx, bucketName, region)
	if err != nil {
		return err
	}
	defer func() {
		fmt.Println("🔑 Cleaning up access key...")
		appClient.DeleteBucketAccessKey(ctx, bucketName, keyID, region)
	}()

	// Build S3 client with the bucket access key credentials
	s3svc := s3.New(s3.Options{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(keyID, secret, ""),
	})

	f, err := os.Open(tmpPath)
	if err != nil {
		return err
	}
	defer f.Close()

	fi, _ := f.Stat()
	fmt.Printf("📤 Uploading %s (%d bytes)...\n", assetName, fi.Size())

	var uploadErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
			f.Seek(0, 0)
		}
		_, uploadErr = s3svc.PutObject(ctx, &s3.PutObjectInput{
			Bucket:        &bucketName,
			Key:           &assetName,
			Body:          f,
			ContentLength: awssdk.Int64(fi.Size()),
		})
		if uploadErr == nil {
			break
		}
	}
	if uploadErr != nil {
		return fmt.Errorf("upload to bucket: %w", uploadErr)
	}

	fmt.Printf("✅ Deployed %s to %s/%s (bucket: %s)\n", assetName, appName, envName, bucketName)
	return nil
}
