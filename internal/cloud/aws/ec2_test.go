package aws

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/dacort/cloud-burst-controller/internal/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEC2Client implements the ec2API interface for testing.
type mockEC2Client struct {
	runInstancesFunc       func(ctx context.Context, input *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	terminateInstancesFunc func(ctx context.Context, input *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	describeInstancesFunc  func(ctx context.Context, input *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

func (m *mockEC2Client) RunInstances(ctx context.Context, input *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	return m.runInstancesFunc(ctx, input, opts...)
}

func (m *mockEC2Client) TerminateInstances(ctx context.Context, input *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	return m.terminateInstancesFunc(ctx, input, opts...)
}

func (m *mockEC2Client) DescribeInstances(ctx context.Context, input *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.describeInstancesFunc(ctx, input, opts...)
}

func strPtr(s string) *string { return &s }

func TestLaunchNode_SetsCorrectParams(t *testing.T) {
	var capturedInput *ec2.RunInstancesInput
	mock := &mockEC2Client{
		runInstancesFunc: func(ctx context.Context, input *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
			capturedInput = input
			return &ec2.RunInstancesOutput{
				Instances: []ec2types.Instance{
					{InstanceId: strPtr("i-abc123")},
				},
			}, nil
		},
	}

	provider := &EC2Provider{client: mock}
	id, err := provider.LaunchNode(context.Background(), cloud.LaunchOptions{
		Name:             "burst-general-abc123",
		AMI:              "ami-test",
		InstanceType:     "m6i.large",
		SubnetID:         "subnet-test",
		SecurityGroupIDs: []string{"sg-test"},
		VolumeSize:       30,
		VolumeType:       "gp3",
		UserData:         "dGVzdA==",
		Tags:             map[string]string{"Environment": "test"},
	})

	require.NoError(t, err)
	assert.Equal(t, "i-abc123", id)
	assert.Equal(t, "ami-test", *capturedInput.ImageId)
	assert.Equal(t, "subnet-test", *capturedInput.SubnetId)
	assert.Equal(t, ec2types.InstanceType("m6i.large"), capturedInput.InstanceType)
	assert.Equal(t, []string{"sg-test"}, capturedInput.SecurityGroupIds)
	assert.Equal(t, "dGVzdA==", *capturedInput.UserData)
}

func TestTerminateNode(t *testing.T) {
	var capturedInput *ec2.TerminateInstancesInput
	mock := &mockEC2Client{
		terminateInstancesFunc: func(ctx context.Context, input *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
			capturedInput = input
			return &ec2.TerminateInstancesOutput{}, nil
		},
	}

	provider := &EC2Provider{client: mock}
	err := provider.TerminateNode(context.Background(), "i-abc123")

	require.NoError(t, err)
	assert.Equal(t, "i-abc123", capturedInput.InstanceIds[0])
}

func TestListManagedNodes(t *testing.T) {
	launchTime := time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC)
	mock := &mockEC2Client{
		describeInstancesFunc: func(ctx context.Context, input *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
			return &ec2.DescribeInstancesOutput{
				Reservations: []ec2types.Reservation{
					{
						Instances: []ec2types.Instance{
							{
								InstanceId: strPtr("i-abc123"),
								State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
								LaunchTime: &launchTime,
								Tags: []ec2types.Tag{
									{Key: strPtr("Name"), Value: strPtr("burst-general-abc123")},
									{Key: strPtr("burst.homelab.dev/managed"), Value: strPtr("true")},
								},
							},
						},
					},
				},
			}, nil
		},
	}

	provider := &EC2Provider{client: mock}
	nodes, err := provider.ListManagedNodes(context.Background())

	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "i-abc123", nodes[0].InstanceID)
	assert.Equal(t, "burst-general-abc123", nodes[0].Name)
	assert.Equal(t, "running", nodes[0].State)
	assert.Equal(t, launchTime, nodes[0].LaunchedAt)
}
