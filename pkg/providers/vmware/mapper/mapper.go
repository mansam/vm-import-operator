package mapper

import (
	"context"
	v2vv1alpha1 "github.com/kubevirt/vm-import-operator/pkg/apis/v2v/v1alpha1"
	"github.com/kubevirt/vm-import-operator/pkg/utils"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/client-go/api/v1"
	"strconv"
	"strings"
)

const (
	vmNamePrefix      = "vmware-"
	networkTypePod    = "pod"
	networkTypeMultus = "multus"
	VmwareDescription = "vmware-description"
	LabelTag          = "tags"
	)

var BiosTypeMapping = map[string]*kubevirtv1.Bootloader {
	"efi":  &kubevirtv1.Bootloader{EFI: &kubevirtv1.EFI{}},
	"bios": &kubevirtv1.Bootloader{BIOS: &kubevirtv1.BIOS{}},
}

type VmwareMapper struct {
	vm *object.VirtualMachine
	vmId string
	vmProperties *mo.VirtualMachine
	hostProperties *mo.HostSystem
	namespace string
	mappings *v2vv1alpha1.VmwareMappings
}

func (r *VmwareMapper) getVmProperties() (*mo.VirtualMachine, error) {
	if r.vmProperties == nil {
		vmProperties := mo.VirtualMachine{}
		err := r.vm.Properties(context.TODO(), types.ManagedObjectReference{
			Type:  "VirtualMachine",
			Value: r.vmId,
		}, nil, vmProperties)
		if err != nil {
			return nil, err
		}
		r.vmProperties = &vmProperties
	}

	return r.vmProperties, nil
}

func (r *VmwareMapper) getHostProperties() (*mo.HostSystem, error) {
	if r.hostProperties == nil {
		hostProperties := mo.HostSystem{}

		hostSystem, err := r.vm.HostSystem(context.TODO())
		if err != nil {
			return nil, err
		}
		err = hostSystem.Properties(context.TODO(), hostSystem.Reference(), nil, hostProperties)
		if err != nil {
			return nil, err
		}
		r.hostProperties = &hostProperties
	}

	return r.hostProperties, nil
}

// ResolveVMName resolves the target VM name
func (r *VmwareMapper) ResolveVMName(targetVMName *string) *string {
	if targetVMName != nil {
		return targetVMName
	}

	name, err := utils.NormalizeName(r.vm.Name())
	if err != nil {
		return nil
	}

	return &name
}

func (r *VmwareMapper) CreateEmptyVM(vmName *string) *kubevirtv1.VirtualMachine {
	return &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"app": *vmName,
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"kubevirt.io/domain":  *vmName,
						"vm.kubevirt.io/name": *vmName,
					},
				},
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Domain: kubevirtv1.DomainSpec{},
				},
			},
		},
	}
}

func (r *VmwareMapper) MapVM(targetVmName *string, vmSpec *kubevirtv1.VirtualMachine) (*kubevirtv1.VirtualMachine, error) {
	// Set Namespace
	vmSpec.ObjectMeta.Namespace = r.namespace

	// Map name
	if targetVmName == nil {
		vmSpec.ObjectMeta.GenerateName = vmNamePrefix
	} else {
		vmSpec.ObjectMeta.Name = *targetVmName
	}
	vmProperties, err := r.getVmProperties()
	if err != nil {
		return nil, err
	}
	hostProperties, err := r.getHostProperties()
	if err != nil {
		return nil, err
	}
	// Map hostname
	vmSpec.Spec.Template.Spec.Hostname = vmProperties.Guest.HostName
	vmSpec.Spec.Template.Spec.Domain.CPU = r.mapCPUTopology(vmProperties)
	vmSpec.Spec.Template.Spec.Domain.Firmware = r.mapFirmware(vmProperties)
	reservations, err := r.mapResourceReservations(vmProperties)
	if err != nil {
		return nil, err
	}
	vmSpec.Spec.Template.Spec.Domain.Resources = reservations
	// Map labels like vm tags
	vmSpec.ObjectMeta.Labels = r.mapLabels(vmSpec.ObjectMeta.Labels, vmProperties)
	// Map annotations
	vmSpec.ObjectMeta.Annotations = r.mapAnnotations(vmProperties)

	// Map clock
	vmSpec.Spec.Template.Spec.Domain.Clock = r.mapClock(hostProperties)

	// Map networks
	vmSpec.Spec.Template.Spec.Networks = r.mapNetworks(vmProperties)
	networkToType := r.mapNetworksToTypes(vmSpec.Spec.Template.Spec.Networks)
	vmSpec.Spec.Template.Spec.Domain.Devices.Interfaces = r.mapNetworkInterfaces(vmProperties, networkToType)
	
	return vmSpec, nil
}

func (r *VmwareMapper) mapLabels(vmLabels map[string]string, vmProperties *mo.VirtualMachine) map[string]string {
	var labels map[string]string
	if vmLabels == nil {
		labels = map[string]string{}
	} else {
		labels = vmLabels
	}

	var tagList []string
	for _, tag := range vmProperties.Tag {
		tagList = append(tagList, tag.Key)
	}
	labels[LabelTag] = strings.Join(tagList, ",")
	return labels
}

func (r *VmwareMapper) mapAnnotations(vmProperties *mo.VirtualMachine) map[string]string {
	annotations := map[string]string{}
	annotations[VmwareDescription] = vmProperties.Config.Annotation
	return annotations
}

func (r *VmwareMapper) mapNetworkInterfaces(vmProperties *mo.VirtualMachine, networkToType map[string]string) []kubevirtv1.Interface {
	var interfaces []kubevirtv1.Interface

	for _, guestInterface := range vmProperties.Guest.Net {
		kubevirtInterface := kubevirtv1.Interface{}
		kubevirtInterface.MacAddress = guestInterface.MacAddress
		switch networkToType[guestInterface.Network] {
		case networkTypeMultus:
			kubevirtInterface.Bridge = &kubevirtv1.InterfaceBridge{}
		case networkTypePod:
			kubevirtInterface.Masquerade = &kubevirtv1.InterfaceMasquerade{}
		}
		interfaces = append(interfaces, kubevirtInterface)
	}

	return interfaces
}

func (r *VmwareMapper) mapNetworks(vmProperties *mo.VirtualMachine) []kubevirtv1.Network {
	var kubevirtNetworks []kubevirtv1.Network
	for _, network := range vmProperties.Network {
		kubevirtNet := kubevirtv1.Network{}

		for _, mapping := range *r.mappings.NetworkMappings {
			if mapping.Source.Name != nil && network.Value == *mapping.Source.Name {
				if *mapping.Type == networkTypePod {
					kubevirtNet.Pod = &kubevirtv1.PodNetwork{}
				} else if *mapping.Type == networkTypeMultus {
					kubevirtNet.Multus = &kubevirtv1.MultusNetwork{
						NetworkName: mapping.Target.Name,
					}
				}
			}
		}
		kubevirtNet.Name, _ = utils.NormalizeName(network.Value)
		kubevirtNetworks = append(kubevirtNetworks, kubevirtNet)
	}

	return kubevirtNetworks
}

func (r *VmwareMapper) mapNetworksToTypes(networks []kubevirtv1.Network) map[string]string {
	networkToType := make(map[string]string)
	for _, network := range networks {
		if network.Multus != nil {
			networkToType[network.Name] = networkTypeMultus
		} else if network.Pod != nil {
			networkToType[network.Name] = networkTypePod
		}
	}
	return networkToType
}

func (r *VmwareMapper) mapFirmware(vmProperties *mo.VirtualMachine) *kubevirtv1.Firmware {
	firmwareSpec := &kubevirtv1.Firmware{}
	firmwareSpec.Bootloader = BiosTypeMapping[vmProperties.Config.Firmware]
	firmwareSpec.Serial = vmProperties.Config.InstanceUuid
	return firmwareSpec
}

func (r *VmwareMapper) mapResourceReservations(vmProperties *mo.VirtualMachine) (kubevirtv1.ResourceRequirements, error) {
	reqs := kubevirtv1.ResourceRequirements{}

	reservation := *vmProperties.ResourceConfig.MemoryAllocation.Reservation
	resString := strconv.FormatInt(reservation, 10) + "Mi"
	resQuantity, err := resource.ParseQuantity(resString)
	if err != nil {
		return reqs, err
	}
	reqs.Requests = map[corev1.ResourceName]resource.Quantity{
		corev1.ResourceMemory: resQuantity,
	}

	limit := *vmProperties.ResourceConfig.MemoryAllocation.Limit
	limitString := strconv.FormatInt(limit, 10) + "Mi"
	limitQuantity, err := resource.ParseQuantity(limitString)
	if err != nil {
		return reqs, err
	}
	reqs.Limits = map[corev1.ResourceName]resource.Quantity{
		corev1.ResourceMemory: limitQuantity,
	}

	return reqs, nil
}

func (r *VmwareMapper) mapCPUTopology(vmProperties *mo.VirtualMachine) *kubevirtv1.CPU {
	cpu := &kubevirtv1.CPU{}
	cpu.Sockets = uint32(vmProperties.Config.Hardware.NumCPU)
	cpu.Cores = uint32(vmProperties.Config.Hardware.NumCoresPerSocket)
	return cpu
}

func (r *VmwareMapper) mapClock(hostProperties *mo.HostSystem) (*kubevirtv1.Clock) {
	offset := &kubevirtv1.ClockOffsetUTC{}
	offsetSeconds := int(hostProperties.Config.DateTimeInfo.TimeZone.GmtOffset)
	offset.OffsetSeconds = &offsetSeconds
	clock := &kubevirtv1.Clock{Timer: &kubevirtv1.Timer{}}
	clock.UTC = offset
	return clock
}