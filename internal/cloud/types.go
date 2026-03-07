package cloud

import "time"

// LaunchOptions configures a new cloud instance.
type LaunchOptions struct {
	Name             string
	AMI              string
	InstanceType     string
	SubnetID         string
	SecurityGroupIDs []string
	VolumeSize       int32
	VolumeType       string
	UserData         string // base64-encoded
	Tags             map[string]string
}

// CloudNode represents a cloud instance managed by the controller.
type CloudNode struct {
	InstanceID   string
	InstanceType string
	Name         string
	State        string // running, stopped, terminated, etc.
	LaunchedAt   time.Time
	Tags         map[string]string
}
