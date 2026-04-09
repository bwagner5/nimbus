package resources

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

const AppBucketPrefix = "ls-app-"

type LightsailApplication struct {
	id, name, bucket, region, state string
	envCount                        int
	targetCount                     int
}

func (a LightsailApplication) ID() string     { return a.id }
func (a LightsailApplication) Name() string   { return a.name }
func (a LightsailApplication) Status() string { return a.state }
func (a LightsailApplication) Region() string { return a.region }
func (a LightsailApplication) Columns() []string {
	return []string{"NAME", "STATE", "BUCKET", "REGION"}
}
func (a LightsailApplication) Values() []string {
	return []string{a.name, a.state, a.bucket, a.region}
}
func (a LightsailApplication) Bucket() string { return a.bucket }

// ParseAppName extracts the app name from a bucket name like ls-app-123456-myapp
func ParseAppName(bucketName string) string {
	if !strings.HasPrefix(bucketName, AppBucketPrefix) {
		return ""
	}
	rest := strings.TrimPrefix(bucketName, AppBucketPrefix)
	// Skip account-id (first segment before -)
	parts := strings.SplitN(rest, "-", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

type LightsailApplicationProvider struct {
	client *aws.Client
}

func NewLightsailApplicationProvider(client *aws.Client) *LightsailApplicationProvider {
	return &LightsailApplicationProvider{client: client}
}

func (p *LightsailApplicationProvider) Kind() string { return "lightsail/applications" }

func (p *LightsailApplicationProvider) Headers() []string {
	return []string{"NAME", "STATE", "BUCKET", "REGION"}
}

func (p *LightsailApplicationProvider) Fetch(ctx context.Context, region string) ([]Resource, error) {
	svc := lightsail.NewFromConfig(p.client.WithRegion(region).Config())
	out, err := svc.GetBuckets(ctx, &lightsail.GetBucketsInput{})
	if err != nil {
		return nil, err
	}
	var res []Resource
	for _, b := range out.Buckets {
		if b.Name == nil || !strings.HasPrefix(*b.Name, AppBucketPrefix) {
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
		res = append(res, LightsailApplication{
			id:     *b.Arn,
			name:   name,
			bucket: *b.Name,
			region: r,
			state:  state,
		})
	}
	return res, nil
}
