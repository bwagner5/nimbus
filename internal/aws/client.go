package aws

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

type Client struct {
	cfg aws.Config
}

func NewClient(ctx context.Context, region string) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg}, nil
}

func (c *Client) Config() aws.Config {
	return c.cfg
}

func (c *Client) WithRegion(region string) *Client {
	cfg := c.cfg.Copy()
	cfg.Region = region
	return &Client{cfg: cfg}
}

func (c *Client) DefaultRegion() string {
	if r := os.Getenv("AWS_REGION"); r != "" {
		return r
	}
	if r := os.Getenv("AWS_DEFAULT_REGION"); r != "" {
		return r
	}
	return c.cfg.Region
}

func (c *Client) FetchRegions(ctx context.Context) ([]string, error) {
	svc := ec2.NewFromConfig(c.cfg)
	out, err := svc.DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	if err != nil {
		return nil, err
	}
	var regions []string
	for _, r := range out.Regions {
		regions = append(regions, *r.RegionName)
	}
	return regions, nil
}

func (c *Client) FetchRegionsSorted(ctx context.Context) ([]string, error) {
	regions, err := c.FetchRegions(ctx)
	if err != nil {
		return nil, err
	}
	return SortRegions(regions, c.DefaultRegion()), nil
}

func SortRegions(regions []string, preferred string) []string {
	prefix := regionPrefix(preferred)
	sort.SliceStable(regions, func(i, j int) bool {
		pi, pj := regionPrefix(regions[i]), regionPrefix(regions[j])
		// Preferred region first
		if regions[i] == preferred {
			return true
		}
		if regions[j] == preferred {
			return false
		}
		// Same prefix as preferred comes next
		if pi == prefix && pj != prefix {
			return true
		}
		if pj == prefix && pi != prefix {
			return false
		}
		// Group by prefix
		if pi != pj {
			return pi < pj
		}
		return regions[i] < regions[j]
	})
	return regions
}

func regionPrefix(r string) string {
	parts := strings.Split(r, "-")
	if len(parts) >= 2 {
		return parts[0] + "-" + parts[1]
	}
	return r
}
