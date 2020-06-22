package mapper

import (
	"context"
	"fmt"
	v2vv1alpha1 "github.com/kubevirt/vm-import-operator/pkg/apis/v2v/v1alpha1"
	"github.com/kubevirt/vm-import-operator/pkg/utils"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/client-go/api/v1"
	cdiv1 "kubevirt.io/containerized-data-importer/pkg/apis/core/v1alpha1"
	"strconv"
	"strings"
)

const (
	vmNamePrefix      = "vmware-"
	networkTypePod    = "pod"
	networkTypeMultus = "multus"
	VmwareDescription = "vmware-description"
	LabelTag          = "tags"
	// DefaultStorageClassTargetName define the storage target name value that forces using default storage class
	DefaultStorageClassTargetName = ""
	cdiAPIVersion = "cdi.kubevirt.io/v1alpha1"
	dataVolumeKind = "DataVolume"
	busTypeSata = "sata"
	busTypeSCSI = "scsi"
)

var BiosTypeMapping = map[string]*kubevirtv1.Bootloader {
	"efi":  {EFI:  &kubevirtv1.EFI{}},
	"bios": {BIOS: &kubevirtv1.BIOS{}},
}

type VmwareMapper struct {
	vm *object.VirtualMachine
	vmProperties *mo.VirtualMachine
	hostProperties *mo.HostSystem
	namespace string
	mappings *v2vv1alpha1.VmwareMappings
	disks *[]Disk
	disksByDVName map[string]Disk
}

type Disk struct {
	id string
	name string
	bus string
	capacity resource.Quantity
}

func (r *VmwareMapper) buildDisks() error {
	if r.disks != nil {
		return nil
	}

	disks := make([]Disk, 0)
	devices, err := r.vm.Device(context.TODO())
	if err != nil {
		return err
	}
	for _, device := range devices {
		if virtualDisk, ok := device.(*types.VirtualDisk); ok {
			var capacityInBytes int64
			if virtualDisk.CapacityInBytes > 0 {
				capacityInBytes = virtualDisk.CapacityInBytes
			} else {
				capacityInBytes = virtualDisk.CapacityInKB * 1024
			}

			diskSizeConverted, err := utils.FormatBytes(capacityInBytes)
			if err != nil {
				return err
			}
			capacity, err := resource.ParseQuantity(diskSizeConverted)
			if err != nil {
				return err
			}

			var bus string
			controller := devices.FindByKey(virtualDisk.GetVirtualDevice().ControllerKey)
			if controller == nil {
				bus = busTypeSata
			} else {
				switch controller.(type) {
				case types.BaseVirtualSCSIController:
					bus = busTypeSCSI
					break
				case types.BaseVirtualSATAController:
					bus = busTypeSata
				default:
					bus = busTypeSata
				}
			}
			var id string
			if virtualDisk.VDiskId != nil && virtualDisk.VDiskId.Id != "" {
				id = virtualDisk.VDiskId.Id
			} else if virtualDisk.DiskObjectId != "" {
				id = virtualDisk.DiskObjectId
			} else {
				id = virtualDisk.DeviceInfo.GetDescription().Label
			}
			disk := Disk{
				id: id,
				name: virtualDisk.DeviceInfo.GetDescription().Label,
				capacity: capacity,
				bus: bus,
			}

			disks = append(disks, disk)
		}
	}
	r.disks = &disks
	return nil
}

func buildDataVolumeName(targetVMName string, diskAttachID string) string {
	dvName, _ := utils.NormalizeName(targetVMName + "-" + diskAttachID)
	return dvName
}

func (r *VmwareMapper) getStorageClassForDisk(disk *Disk) *string {
	if r.mappings.DiskMappings != nil {
		for _, mapping := range *r.mappings.DiskMappings {
			targetName := mapping.Target.Name
			if mapping.Source.ID != nil {
				if disk.id == *mapping.Source.ID {
					if targetName != DefaultStorageClassTargetName {
						return &targetName
					}
				}
			}
			if mapping.Source.Name != nil {
				if disk.name == *mapping.Source.Name {
					if targetName != DefaultStorageClassTargetName {
						return &targetName
					}
				}
			}
		}
	}
	return nil
}

func (r *VmwareMapper) MapDataVolumes(targetVMName *string) (map[string]cdiv1.DataVolume, error) {
	err := r.buildDisks()
	if err != nil {
		return nil, err
	}

	dvs := make(map[string]cdiv1.DataVolume)

	for _, disk := range *r.disks {
		dvName := buildDataVolumeName(*targetVMName, disk.id)

		dvs[dvName] = cdiv1.DataVolume{
			TypeMeta: metav1.TypeMeta{
				APIVersion: cdiAPIVersion,
				Kind:       dataVolumeKind,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      dvName,
				Namespace: r.namespace,
			},
			Spec: cdiv1.DataVolumeSpec{
				Source: cdiv1.DataVolumeSource{
					// TODO: figure out CDI vmware source
				},
				PVC: &corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{
						corev1.ReadWriteOnce,
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: disk.capacity,
						},
					},
				},
			},
		}
		sdClass := r.getStorageClassForDisk(&disk)
		if sdClass != nil {
			dvs[dvName].Spec.PVC.StorageClassName = sdClass
		}
		r.disksByDVName[dvName] = disk
	}
	return dvs, nil
}

func (r *VmwareMapper) MapDisks(vmSpec *kubevirtv1.VirtualMachine, dvs map[string]cdiv1.DataVolume) {
	volumes := make([]kubevirtv1.Volume, len(dvs))
	i := 0
	for _, dv := range dvs {
		volumes[i] = kubevirtv1.Volume{
			Name: fmt.Sprintf("dv-#{i}"),
			VolumeSource: kubevirtv1.VolumeSource{
				DataVolume: &kubevirtv1.DataVolumeSource{
					Name: dv.Name,
				},
			},
		}
		i++
	}

	i = 0
	disks := make([]kubevirtv1.Disk, len(dvs))
	for dvName := range dvs {
		disk := r.disksByDVName[dvName]
		disks[i] = kubevirtv1.Disk{
			Name: fmt.Sprintf("dv-%v", i),
			DiskDevice: kubevirtv1.DiskDevice{
				Disk: &kubevirtv1.DiskTarget{
					Bus: disk.bus,
				},
			},
		}
		i++
	}

	vmSpec.Spec.Template.Spec.Volumes = volumes
	vmSpec.Spec.Template.Spec.Domain.Devices.Disks = disks
}

func NewVmwareMapper(vm *object.VirtualMachine, vmProperties *mo.VirtualMachine, mappings *v2vv1alpha1.VmwareMappings, namespace string) *VmwareMapper {
	return &VmwareMapper{
		vm: vm,
		vmProperties: vmProperties,
		mappings: mappings,
		namespace: namespace,
		disksByDVName: make(map[string]Disk),
	}
}

func (r *VmwareMapper) getHostProperties() (*mo.HostSystem, error) {
	if r.hostProperties == nil {
		hostProperties := &mo.HostSystem{}

		hostSystem, err := r.vm.HostSystem(context.TODO())
		if err != nil {
			return nil, err
		}
		err = hostSystem.Properties(context.TODO(), hostSystem.Reference(), nil, hostProperties)
		if err != nil {
			return nil, err
		}
		r.hostProperties = hostProperties
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
	if vmSpec.Spec.Template == nil {
		vmSpec.Spec.Template = &kubevirtv1.VirtualMachineInstanceTemplateSpec{}
	}
	// Set Namespace
	vmSpec.ObjectMeta.Namespace = r.namespace

	// Map name
	if targetVmName == nil {
		vmSpec.ObjectMeta.GenerateName = vmNamePrefix
	} else {
		vmSpec.ObjectMeta.Name = *targetVmName
	}
	hostProperties, err := r.getHostProperties()
	if err != nil {
		return nil, err
	}
	// Map hostname
	vmSpec.Spec.Template.Spec.Hostname = r.vmProperties.Guest.HostName
	vmSpec.Spec.Template.Spec.Domain.CPU = r.mapCPUTopology()
	vmSpec.Spec.Template.Spec.Domain.Firmware = r.mapFirmware()
	reservations, err := r.mapResourceReservations()
	if err != nil {
		return nil, err
	}
	vmSpec.Spec.Template.Spec.Domain.Resources = reservations
	// Map labels like vm tags
	vmSpec.ObjectMeta.Labels = r.mapLabels(vmSpec.ObjectMeta.Labels)
	// Map annotations
	vmSpec.ObjectMeta.Annotations = r.mapAnnotations()

	// Map clock
	vmSpec.Spec.Template.Spec.Domain.Clock = r.mapClock(hostProperties)

	// Map networks
	vmSpec.Spec.Template.Spec.Networks = r.mapNetworks()
	networkToType := r.mapNetworksToTypes(vmSpec.Spec.Template.Spec.Networks)
	vmSpec.Spec.Template.Spec.Domain.Devices.Interfaces = r.mapNetworkInterfaces(networkToType)

	return vmSpec, nil
}

func (r *VmwareMapper) mapLabels(vmLabels map[string]string) map[string]string {
	var labels map[string]string
	if vmLabels == nil {
		labels = map[string]string{}
	} else {
		labels = vmLabels
	}

	var tagList []string
	for _, tag := range r.vmProperties.Tag {
		tagList = append(tagList, tag.Key)
	}
	labels[LabelTag] = strings.Join(tagList, ",")
	return labels
}

func (r *VmwareMapper) mapAnnotations() map[string]string {
	annotations := map[string]string{}
	annotations[VmwareDescription] = r.vmProperties.Config.Annotation
	return annotations
}

func (r *VmwareMapper) mapNetworkInterfaces(networkToType map[string]string) []kubevirtv1.Interface {
	var interfaces []kubevirtv1.Interface

	for _, guestInterface := range r.vmProperties.Guest.Net {
		kubevirtInterface := kubevirtv1.Interface{}
		kubevirtInterface.MacAddress = guestInterface.MacAddress
		kubevirtInterface.Name = guestInterface.Network
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

func (r *VmwareMapper) mapNetworks() []kubevirtv1.Network {
	var kubevirtNetworks []kubevirtv1.Network
	for _, iface := range r.vmProperties.Guest.Net {
		kubevirtNet := kubevirtv1.Network{}

		for _, mapping := range *r.mappings.NetworkMappings {
			if mapping.Source.Name != nil && iface.Network == *mapping.Source.Name {
				if *mapping.Type == networkTypePod {
					kubevirtNet.Pod = &kubevirtv1.PodNetwork{}
				} else if *mapping.Type == networkTypeMultus {
					kubevirtNet.Multus = &kubevirtv1.MultusNetwork{
						NetworkName: mapping.Target.Name,
					}
				}
			}
		}
		kubevirtNet.Name, _ = utils.NormalizeName(iface.Network)
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

func (r *VmwareMapper) mapFirmware() *kubevirtv1.Firmware {
	firmwareSpec := &kubevirtv1.Firmware{}
	firmwareSpec.Bootloader = BiosTypeMapping[r.vmProperties.Config.Firmware]
	firmwareSpec.Serial = r.vmProperties.Config.InstanceUuid
	return firmwareSpec
}

func (r *VmwareMapper) mapResourceReservations() (kubevirtv1.ResourceRequirements, error) {
	reqs := kubevirtv1.ResourceRequirements{}
	if r.vmProperties.ResourceConfig == nil {return reqs, nil
	}

	reservation := *r.vmProperties.ResourceConfig.MemoryAllocation.Reservation
	resString := strconv.FormatInt(reservation, 10) + "Mi"
	resQuantity, err := resource.ParseQuantity(resString)
	if err != nil {
		return reqs, err
	}
	reqs.Requests = map[corev1.ResourceName]resource.Quantity{
		corev1.ResourceMemory: resQuantity,
	}

	limit := *r.vmProperties.ResourceConfig.MemoryAllocation.Limit
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

func (r *VmwareMapper) mapCPUTopology() *kubevirtv1.CPU {
	cpu := &kubevirtv1.CPU{}
	cpu.Sockets = uint32(r.vmProperties.Config.Hardware.NumCPU)
	cpu.Cores = uint32(r.vmProperties.Config.Hardware.NumCoresPerSocket)
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