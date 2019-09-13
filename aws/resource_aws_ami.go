package aws

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ec2"

	"github.com/hashicorp/terraform/helper/hashcode"
	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
)

const (
	AWSAMIRetryTimeout       = 40 * time.Minute
	AWSAMIDeleteRetryTimeout = 90 * time.Minute
	AWSAMIRetryDelay         = 5 * time.Second
	AWSAMIRetryMinTimeout    = 3 * time.Second
)

func resourceAwsAmi() *schema.Resource {
	return &schema.Resource{
		Create: resourceAwsAmiCreate,
		// The Read, Update and Delete operations are shared with aws_ami_copy
		// and aws_ami_from_instance, since they differ only in how the image
		// is created.
		Read:   resourceAwsAmiRead,
		Update: resourceAwsAmiUpdate,
		Delete: resourceAwsAmiDelete,

		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(AWSAMIRetryTimeout),
			Update: schema.DefaultTimeout(AWSAMIRetryTimeout),
			Delete: schema.DefaultTimeout(AWSAMIDeleteRetryTimeout),
		},

		Schema: map[string]*schema.Schema{
			"image_location": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
				ForceNew: true,
			},
			"architecture": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
				Default:  "x86_64",
			},
			"description": {
				Type:     schema.TypeString,
				Optional: true,
			},
			// The following block device attributes intentionally mimick the
			// corresponding attributes on aws_instance, since they have the
			// same meaning.
			// However, we don't use root_block_device here because the constraint
			// on which root device attributes can be overridden for an instance to
			// not apply when registering an AMI.
			"ebs_block_device": {
				Type:     schema.TypeSet,
				Optional: true,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"delete_on_termination": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  true,
							ForceNew: true,
						},

						"device_name": {
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
						},

						"encrypted": {
							Type:     schema.TypeBool,
							Optional: true,
							ForceNew: true,
						},

						"iops": {
							Type:     schema.TypeInt,
							Optional: true,
							ForceNew: true,
						},

						"snapshot_id": {
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},

						"volume_size": {
							Type:     schema.TypeInt,
							Optional: true,
							Computed: true,
							ForceNew: true,
						},

						"volume_type": {
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
							Default:  "standard",
						},
					},
				},
				Set: func(v interface{}) int {
					var buf bytes.Buffer
					m := v.(map[string]interface{})
					buf.WriteString(fmt.Sprintf("%s-", m["device_name"].(string)))
					buf.WriteString(fmt.Sprintf("%s-", m["snapshot_id"].(string)))
					return hashcode.String(buf.String())
				},
			},
			"ena_support": {
				Type:     schema.TypeBool,
				Optional: true,
				ForceNew: true,
			},
			"ephemeral_block_device": {
				Type:     schema.TypeSet,
				Optional: true,
				Computed: true,
				ForceNew: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"device_name": {
							Type:     schema.TypeString,
							Required: true,
						},

						"virtual_name": {
							Type:     schema.TypeString,
							Required: true,
						},
					},
				},
				Set: func(v interface{}) int {
					var buf bytes.Buffer
					m := v.(map[string]interface{})
					buf.WriteString(fmt.Sprintf("%s-", m["device_name"].(string)))
					buf.WriteString(fmt.Sprintf("%s-", m["virtual_name"].(string)))
					return hashcode.String(buf.String())
				},
			},
			"kernel_id": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},
			// Not a public attribute; used to let the aws_ami_copy and aws_ami_from_instance
			// resources record that they implicitly created new EBS snapshots that we should
			// now manage. Not set by aws_ami, since the snapshots used there are presumed to
			// be independently managed.
			"manage_ebs_snapshots": {
				Type:     schema.TypeBool,
				Computed: true,
				ForceNew: true,
			},
			"name": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"ramdisk_id": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},
			"root_device_name": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},
			"root_snapshot_id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"sriov_net_support": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
				Default:  "simple",
			},
			"tags": tagsSchema(),
			"virtualization_type": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
				Default:  "paravirtual",
			},
		},
	}
}

func resourceAwsAmiCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*AWSClient).ec2conn

	req := &ec2.RegisterImageInput{
		Name:               aws.String(d.Get("name").(string)),
		Description:        aws.String(d.Get("description").(string)),
		Architecture:       aws.String(d.Get("architecture").(string)),
		ImageLocation:      aws.String(d.Get("image_location").(string)),
		RootDeviceName:     aws.String(d.Get("root_device_name").(string)),
		SriovNetSupport:    aws.String(d.Get("sriov_net_support").(string)),
		VirtualizationType: aws.String(d.Get("virtualization_type").(string)),
		EnaSupport:         aws.Bool(d.Get("ena_support").(bool)),
	}

	if kernelId := d.Get("kernel_id").(string); kernelId != "" {
		req.KernelId = aws.String(kernelId)
	}
	if ramdiskId := d.Get("ramdisk_id").(string); ramdiskId != "" {
		req.RamdiskId = aws.String(ramdiskId)
	}

	ebsBlockDevsSet := d.Get("ebs_block_device").(*schema.Set)
	ephemeralBlockDevsSet := d.Get("ephemeral_block_device").(*schema.Set)
	for _, ebsBlockDevI := range ebsBlockDevsSet.List() {
		ebsBlockDev := ebsBlockDevI.(map[string]interface{})
		blockDev := &ec2.BlockDeviceMapping{
			DeviceName: aws.String(ebsBlockDev["device_name"].(string)),
			Ebs: &ec2.EbsBlockDevice{
				DeleteOnTermination: aws.Bool(ebsBlockDev["delete_on_termination"].(bool)),
				VolumeType:          aws.String(ebsBlockDev["volume_type"].(string)),
			},
		}
		if iops, ok := ebsBlockDev["iops"]; ok {
			if iop := iops.(int); iop != 0 {
				blockDev.Ebs.Iops = aws.Int64(int64(iop))
			}
		}
		if size, ok := ebsBlockDev["volume_size"]; ok {
			if s := size.(int); s != 0 {
				blockDev.Ebs.VolumeSize = aws.Int64(int64(s))
			}
		}
		encrypted := ebsBlockDev["encrypted"].(bool)
		if snapshotId := ebsBlockDev["snapshot_id"].(string); snapshotId != "" {
			blockDev.Ebs.SnapshotId = aws.String(snapshotId)
			if encrypted {
				return errors.New("can't set both 'snapshot_id' and 'encrypted'")
			}
		} else if encrypted {
			blockDev.Ebs.Encrypted = aws.Bool(true)
		}
		req.BlockDeviceMappings = append(req.BlockDeviceMappings, blockDev)
	}
	for _, ephemeralBlockDevI := range ephemeralBlockDevsSet.List() {
		ephemeralBlockDev := ephemeralBlockDevI.(map[string]interface{})
		blockDev := &ec2.BlockDeviceMapping{
			DeviceName:  aws.String(ephemeralBlockDev["device_name"].(string)),
			VirtualName: aws.String(ephemeralBlockDev["virtual_name"].(string)),
		}
		req.BlockDeviceMappings = append(req.BlockDeviceMappings, blockDev)
	}

	res, err := client.RegisterImage(req)
	if err != nil {
		return err
	}

	id := *res.ImageId
	d.SetId(id)

	_, err = resourceAwsAmiWaitForAvailable(d.Timeout(schema.TimeoutCreate), id, client)
	if err != nil {
		return err
	}

	return resourceAwsAmiUpdate(d, meta)
}

func resourceAwsAmiRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*AWSClient).ec2conn
	id := d.Id()

	req := &ec2.DescribeImagesInput{
		ImageIds: []*string{aws.String(id)},
	}

	var res *ec2.DescribeImagesOutput
	err := resource.Retry(1*time.Minute, func() *resource.RetryError {
		var err error
		res, err = client.DescribeImages(req)
		if err != nil {
			if isAWSErr(err, "InvalidAMIID.NotFound", "") {
				if d.IsNewResource() {
					return resource.RetryableError(err)
				}

				log.Printf("[DEBUG] %s no longer exists, so we'll drop it from the state", id)
				d.SetId("")
				return nil
			}

			return resource.NonRetryableError(err)
		}
		return nil
	})
	if isResourceTimeoutError(err) {
		res, err = client.DescribeImages(req)
	}
	if err != nil {
		return fmt.Errorf("Unable to find AMI after retries: %s", err)
	}

	if len(res.Images) != 1 {
		d.SetId("")
		return nil
	}

	image := res.Images[0]
	state := *image.State

	if state == "pending" {
		// This could happen if a user manually adds an image we didn't create
		// to the state. We'll wait for the image to become available
		// before we continue. We should never take this branch in normal
		// circumstances since we would've waited for availability during
		// the "Create" step.
		image, err = resourceAwsAmiWaitForAvailable(d.Timeout(schema.TimeoutCreate), id, client)
		if err != nil {
			return err
		}
		state = *image.State
	}

	if state == "deregistered" {
		d.SetId("")
		return nil
	}

	if state != "available" {
		return fmt.Errorf("AMI has become %s", state)
	}

	d.Set("name", image.Name)
	d.Set("description", image.Description)
	d.Set("image_location", image.ImageLocation)
	d.Set("architecture", image.Architecture)
	d.Set("kernel_id", image.KernelId)
	d.Set("ramdisk_id", image.RamdiskId)
	d.Set("root_device_name", image.RootDeviceName)
	d.Set("root_snapshot_id", amiRootSnapshotId(image))
	d.Set("sriov_net_support", image.SriovNetSupport)
	d.Set("virtualization_type", image.VirtualizationType)
	d.Set("ena_support", image.EnaSupport)

	var ebsBlockDevs []map[string]interface{}
	var ephemeralBlockDevs []map[string]interface{}

	for _, blockDev := range image.BlockDeviceMappings {
		if blockDev.Ebs != nil {
			ebsBlockDev := map[string]interface{}{
				"device_name":           *blockDev.DeviceName,
				"delete_on_termination": *blockDev.Ebs.DeleteOnTermination,
				"encrypted":             *blockDev.Ebs.Encrypted,
				"iops":                  0,
				"volume_size":           int(*blockDev.Ebs.VolumeSize),
				"volume_type":           *blockDev.Ebs.VolumeType,
			}
			if blockDev.Ebs.Iops != nil {
				ebsBlockDev["iops"] = int(*blockDev.Ebs.Iops)
			}
			// The snapshot ID might not be set.
			if blockDev.Ebs.SnapshotId != nil {
				ebsBlockDev["snapshot_id"] = *blockDev.Ebs.SnapshotId
			}
			ebsBlockDevs = append(ebsBlockDevs, ebsBlockDev)
		} else {
			ephemeralBlockDevs = append(ephemeralBlockDevs, map[string]interface{}{
				"device_name":  *blockDev.DeviceName,
				"virtual_name": *blockDev.VirtualName,
			})
		}
	}

	d.Set("ebs_block_device", ebsBlockDevs)
	d.Set("ephemeral_block_device", ephemeralBlockDevs)

	d.Set("tags", tagsToMap(image.Tags))

	return nil
}

func resourceAwsAmiUpdate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*AWSClient).ec2conn

	d.Partial(true)

	if err := setTags(client, d); err != nil {
		return err
	} else {
		d.SetPartial("tags")
	}

	if d.Get("description").(string) != "" {
		_, err := client.ModifyImageAttribute(&ec2.ModifyImageAttributeInput{
			ImageId: aws.String(d.Id()),
			Description: &ec2.AttributeValue{
				Value: aws.String(d.Get("description").(string)),
			},
		})
		if err != nil {
			return err
		}
		d.SetPartial("description")
	}

	d.Partial(false)

	return resourceAwsAmiRead(d, meta)
}

func resourceAwsAmiDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*AWSClient).ec2conn

	req := &ec2.DeregisterImageInput{
		ImageId: aws.String(d.Id()),
	}

	_, err := client.DeregisterImage(req)
	if err != nil {
		return err
	}

	// If we're managing the EBS snapshots then we need to delete those too.
	if d.Get("manage_ebs_snapshots").(bool) {
		errs := map[string]error{}
		ebsBlockDevsSet := d.Get("ebs_block_device").(*schema.Set)
		req := &ec2.DeleteSnapshotInput{}
		for _, ebsBlockDevI := range ebsBlockDevsSet.List() {
			ebsBlockDev := ebsBlockDevI.(map[string]interface{})
			snapshotId := ebsBlockDev["snapshot_id"].(string)
			if snapshotId != "" {
				req.SnapshotId = aws.String(snapshotId)
				_, err := client.DeleteSnapshot(req)
				if err != nil {
					errs[snapshotId] = err
				}
			}
		}

		if len(errs) > 0 {
			errParts := []string{"Errors while deleting associated EBS snapshots:"}
			for snapshotId, err := range errs {
				errParts = append(errParts, fmt.Sprintf("%s: %s", snapshotId, err))
			}
			errParts = append(errParts, "These are no longer managed by Terraform and must be deleted manually.")
			return errors.New(strings.Join(errParts, "\n"))
		}
	}

	// Verify that the image is actually removed, if not we need to wait for it to be removed
	if err := resourceAwsAmiWaitForDestroy(d.Timeout(schema.TimeoutDelete), d.Id(), client); err != nil {
		return err
	}

	return nil
}

func AMIStateRefreshFunc(client *ec2.EC2, id string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		emptyResp := &ec2.DescribeImagesOutput{}

		resp, err := client.DescribeImages(&ec2.DescribeImagesInput{ImageIds: []*string{aws.String(id)}})
		if err != nil {
			if ec2err, ok := err.(awserr.Error); ok && ec2err.Code() == "InvalidAMIID.NotFound" {
				return emptyResp, "destroyed", nil
			} else if resp != nil && len(resp.Images) == 0 {
				return emptyResp, "destroyed", nil
			} else {
				return emptyResp, "", fmt.Errorf("Error on refresh: %+v", err)
			}
		}

		if resp == nil || resp.Images == nil || len(resp.Images) == 0 {
			return emptyResp, "destroyed", nil
		}

		// AMI is valid, so return it's state
		return resp.Images[0], *resp.Images[0].State, nil
	}
}

func resourceAwsAmiWaitForDestroy(timeout time.Duration, id string, client *ec2.EC2) error {
	log.Printf("Waiting for AMI %s to be deleted...", id)

	stateConf := &resource.StateChangeConf{
		Pending:    []string{"available", "pending", "failed"},
		Target:     []string{"destroyed"},
		Refresh:    AMIStateRefreshFunc(client, id),
		Timeout:    timeout,
		Delay:      AWSAMIRetryDelay,
		MinTimeout: AWSAMIRetryMinTimeout,
	}

	_, err := stateConf.WaitForState()
	if err != nil {
		return fmt.Errorf("Error waiting for AMI (%s) to be deleted: %v", id, err)
	}

	return nil
}

func resourceAwsAmiWaitForAvailable(timeout time.Duration, id string, client *ec2.EC2) (*ec2.Image, error) {
	log.Printf("Waiting for AMI %s to become available...", id)

	stateConf := &resource.StateChangeConf{
		Pending:    []string{"pending"},
		Target:     []string{"available"},
		Refresh:    AMIStateRefreshFunc(client, id),
		Timeout:    timeout,
		Delay:      AWSAMIRetryDelay,
		MinTimeout: AWSAMIRetryMinTimeout,
	}

	info, err := stateConf.WaitForState()
	if err != nil {
		return nil, fmt.Errorf("Error waiting for AMI (%s) to be ready: %v", id, err)
	}
	return info.(*ec2.Image), nil
}
