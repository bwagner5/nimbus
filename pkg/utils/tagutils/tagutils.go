package tagutils

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/samber/lo"
)

// NamespacedTags returns a map of tag key/value pairs in standardized way.
// name is optional to get tags back for a selector
func NamespacedTags(namespace string, name string) map[string]string {
	tags := map[string]string{
		"Namespace": namespace,
		"CreatedBy": "nimbus",
	}
	if name != "" {
		tags["Name"] = name
	}
	return tags
}

func EC2NamespacedTags(namespace, name string) []ec2types.Tag {
	tags := NamespacedTags(namespace, name)
	var ec2Tags []ec2types.Tag
	for k, v := range tags {
		ec2Tags = append(ec2Tags, ec2types.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}
	return ec2Tags
}

func EC2TagsToMap(ec2Tags []ec2types.Tag) map[string]string {
	tags := map[string]string{}
	for _, t := range ec2Tags {
		tags[lo.FromPtr(t.Key)] = lo.FromPtr(t.Value)
	}
	return tags
}
