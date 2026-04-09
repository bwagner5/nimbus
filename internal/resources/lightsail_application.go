package resources

import (
	"context"

	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

// AppBucketPrefix is kept for backward compatibility with existing code.
const AppBucketPrefix = applications.BucketPrefix

// ParseAppName delegates to the applications package.
func ParseAppName(bucketName string) string {
	return applications.ParseAppName(bucketName)
}

type LightsailApplication struct {
	id, name, bucket, region, state string
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
	appClient := applications.NewClient(p.client)
	apps, err := appClient.List(ctx, region)
	if err != nil {
		return nil, err
	}
	var res []Resource
	for _, a := range apps {
		res = append(res, LightsailApplication{
			id:     a.Bucket, // use bucket as ID
			name:   a.Name,
			bucket: a.Bucket,
			region: a.Region,
			state:  a.State,
		})
	}
	return res, nil
}
