package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/dacort/cloud-burst-controller/internal/cloud"
)

// ec2API is the subset of the EC2 client we use, for testability.
type ec2API interface {
	RunInstances(ctx context.Context, input *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	TerminateInstances(ctx context.Context, input *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeInstances(ctx context.Context, input *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// EC2Provider implements cloud.Provider for AWS EC2.
type EC2Provider struct {
	client ec2API
}

// Compile-time check that EC2Provider implements cloud.Provider.
var _ cloud.Provider = (*EC2Provider)(nil)

// NewEC2Provider creates a provider using default AWS SDK config.
func NewEC2Provider(ctx context.Context) (*EC2Provider, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return &EC2Provider{client: ec2.NewFromConfig(cfg)}, nil
}

func (p *EC2Provider) LaunchNode(ctx context.Context, opts cloud.LaunchOptions) (string, error) {
	tags := make([]ec2types.Tag, 0, 2+len(opts.Tags))
	tags = append(tags,
		ec2types.Tag{Key: aws.String("Name"), Value: aws.String(opts.Name)},
		ec2types.Tag{Key: aws.String("burst.homelab.dev/managed"), Value: aws.String("true")},
	)
	for k, v := range opts.Tags {
		tags = append(tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}

	input := &ec2.RunInstancesInput{
		ImageId:          aws.String(opts.AMI),
		InstanceType:     ec2types.InstanceType(opts.InstanceType),
		MinCount:         aws.Int32(1),
		MaxCount:         aws.Int32(1),
		SubnetId:         aws.String(opts.SubnetID),
		SecurityGroupIds: opts.SecurityGroupIDs,
		UserData:         aws.String(opts.UserData),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags:         tags,
			},
		},
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{
			{
				DeviceName: aws.String("/dev/xvda"),
				Ebs: &ec2types.EbsBlockDevice{
					VolumeSize: aws.Int32(opts.VolumeSize),
					VolumeType: ec2types.VolumeType(opts.VolumeType),
				},
			},
		},
		InstanceInitiatedShutdownBehavior: ec2types.ShutdownBehaviorTerminate,
	}

	result, err := p.client.RunInstances(ctx, input)
	if err != nil {
		return "", fmt.Errorf("launching EC2 instance: %w", err)
	}
	if len(result.Instances) == 0 {
		return "", fmt.Errorf("no instances returned from RunInstances")
	}
	return aws.ToString(result.Instances[0].InstanceId), nil
}

func (p *EC2Provider) TerminateNode(ctx context.Context, instanceID string) error {
	_, err := p.client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("terminating EC2 instance %s: %w", instanceID, err)
	}
	return nil
}

func (p *EC2Provider) ListManagedNodes(ctx context.Context) ([]cloud.CloudNode, error) {
	input := &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("tag:burst.homelab.dev/managed"),
				Values: []string{"true"},
			},
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"pending", "running"},
			},
		},
	}

	result, err := p.client.DescribeInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("describing EC2 instances: %w", err)
	}

	var nodes []cloud.CloudNode
	for _, reservation := range result.Reservations {
		for _, instance := range reservation.Instances {
			node := cloud.CloudNode{
				InstanceID:   aws.ToString(instance.InstanceId),
				InstanceType: string(instance.InstanceType),
				State:        string(instance.State.Name),
				Tags:         make(map[string]string),
			}
			if instance.LaunchTime != nil {
				node.LaunchedAt = *instance.LaunchTime
			}
			for _, tag := range instance.Tags {
				node.Tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
				if aws.ToString(tag.Key) == "Name" {
					node.Name = aws.ToString(tag.Value)
				}
			}
			nodes = append(nodes, node)
		}
	}
	return nodes, nil
}

// IsCapacityError returns true if the error is an EC2 capacity-related error
// that should trigger fallback to the next instance type candidate.
func IsCapacityError(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "InsufficientInstanceCapacity",
			"InstanceLimitExceeded",
			"Unsupported":
			return true
		}
	}
	return false
}
