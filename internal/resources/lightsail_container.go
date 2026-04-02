package resources

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

type LightsailContainer struct {
	id, name, state, region, url string
	scale                        int
}

func (c LightsailContainer) ID() string       { return c.id }
func (c LightsailContainer) Name() string     { return c.name }
func (c LightsailContainer) Status() string   { return c.state }
func (c LightsailContainer) Region() string   { return c.region }
func (c LightsailContainer) Columns() []string { return []string{"NAME", "STATE", "SCALE", "URL", "REGION"} }
func (c LightsailContainer) Values() []string {
	return []string{c.name, c.state, fmt.Sprintf("%d", c.scale), c.url, c.region}
}

type LightsailContainerProvider struct {
	client *aws.Client
}

func NewLightsailContainerProvider(client *aws.Client) *LightsailContainerProvider {
	return &LightsailContainerProvider{client: client}
}

func (p *LightsailContainerProvider) Kind() string { return "lightsail/containers" }

func (p *LightsailContainerProvider) Headers() []string {
	return []string{"NAME", "STATE", "SCALE", "URL", "REGION"}
}

func (p *LightsailContainerProvider) Fetch(ctx context.Context, region string) ([]Resource, error) {
	svc := lightsail.NewFromConfig(p.client.WithRegion(region).Config())
	out, err := svc.GetContainerServices(ctx, &lightsail.GetContainerServicesInput{})
	if err != nil {
		return nil, err
	}
	var res []Resource
	for _, c := range out.ContainerServices {
		url := ""
		if c.Url != nil {
			url = *c.Url
		}
		res = append(res, LightsailContainer{
			id:     *c.Arn,
			name:   *c.ContainerServiceName,
			state:  string(c.State),
			region: region,
			scale:  int(*c.Scale),
			url:    url,
		})
	}
	return res, nil
}
