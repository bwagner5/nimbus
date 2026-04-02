package resources

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

type LightsailBucket struct {
	id, name, state, region, url string
}

func (b LightsailBucket) ID() string        { return b.id }
func (b LightsailBucket) Name() string      { return b.name }
func (b LightsailBucket) Status() string    { return b.state }
func (b LightsailBucket) Region() string    { return b.region }
func (b LightsailBucket) Columns() []string { return []string{"NAME", "STATE", "URL", "REGION"} }
func (b LightsailBucket) Values() []string  { return []string{b.name, b.state, b.url, b.region} }

type LightsailBucketProvider struct {
	client *aws.Client
}

func NewLightsailBucketProvider(client *aws.Client) *LightsailBucketProvider {
	return &LightsailBucketProvider{client: client}
}

func (p *LightsailBucketProvider) Kind() string { return "lightsail/buckets" }

func (p *LightsailBucketProvider) Headers() []string {
	return []string{"NAME", "STATE", "URL", "REGION"}
}

func (p *LightsailBucketProvider) Fetch(ctx context.Context, region string) ([]Resource, error) {
	svc := lightsail.NewFromConfig(p.client.WithRegion(region).Config())
	var res []Resource
	var pageToken *string
	for {
		out, err := svc.GetBuckets(ctx, &lightsail.GetBucketsInput{PageToken: pageToken})
		if err != nil {
			return res, err
		}
		for _, b := range out.Buckets {
			state := "active"
			if b.State != nil && b.State.Code != nil {
				state = *b.State.Code
			}
			res = append(res, LightsailBucket{
				id:     *b.Arn,
				name:   *b.Name,
				state:  state,
				region: region,
				url:    fmt.Sprintf("%s.s3.%s.amazonaws.com", *b.Name, region),
			})
		}
		if out.NextPageToken == nil {
			break
		}
		pageToken = out.NextPageToken
	}
	return res, nil
}
