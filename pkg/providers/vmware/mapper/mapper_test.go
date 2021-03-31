package mapper_test

import (
	"context"

	"github.com/kubevirt/vm-import-operator/pkg/apis/v2v/v1beta1"
	"github.com/kubevirt/vm-import-operator/pkg/providers/vmware/mapper"
	"github.com/kubevirt/vm-import-operator/pkg/providers/vmware/os"
	"github.com/kubevirt/vm-import-operator/pkg/utils"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	kubevirtv1 "kubevirt.io/client-go/api/v1"
	cdiv1 "kubevirt.io/containerized-data-importer/pkg/apis/core/v1alpha1"
)

var (
	vmMoRef      = "vm-70"
	targetVMName = "basic-vm"
	instanceUID  = "d39a8d6c-ea37-5c91-8979-334e7e07cab6"

	// vm attributes
	memoryReservationStr       = "2048Mi"
	cpuCores             int32 = 1
	cpuSockets           int32 = 4
	gmtOffsetSeconds     int32 = 3600
	machineType                = "q35"

	// networks
	networkName              = "VM Network"
	networkMoRef             = "network-7"
	macAddress               = "00:0c:29:5b:62:35"
	networkNormalizedName, _ = utils.NormalizeName(networkName)
	multusNetwork            = "multus"
	podNetwork               = "pod"

	// disks
	expectedNumDisks  = 2
	diskName1         = "disk-202-0"
	diskBytes1        = 2147483648
	diskOverhead1     = 2272469468 // 5.5% overhead
	diskName2         = "disk-202-1"
	diskBytes2        = 1073741824
	expectedDiskName1 = "d39a8d6c-ea37-5c91-8979-334e7e07cab6-203"
	expectedDiskName2 = "d39a8d6c-ea37-5c91-8979-334e7e07cab6-205"

	volumeModeBlock      = v1.PersistentVolumeBlock
	volumeModeFilesystem = v1.PersistentVolumeFilesystem
	accessModeRWO        = v1.ReadWriteOnce
	accessModeRWM        = v1.ReadWriteMany
)

type mockOsFinder struct{}

func (r mockOsFinder) FindOperatingSystem(_ *mo.VirtualMachine) (string, error) {
	return findOs()
}

var (
	osFinder os.OSFinder = mockOsFinder{}
	findOs   func() (string, error)
)

func prepareVsphereObjects(client *govmomi.Client) (*object.VirtualMachine, *mo.VirtualMachine, *mo.HostSystem) {
	moRef := types.ManagedObjectReference{Type: "VirtualMachine", Value: vmMoRef}
	vm := object.NewVirtualMachine(client.Client, moRef)
	vmProperties := &mo.VirtualMachine{}
	err := vm.Properties(context.TODO(), vm.Reference(), nil, vmProperties)
	Expect(err).To(BeNil())
	host := object.NewHostSystem(client.Client, *vmProperties.Runtime.Host)
	hostProperties := &mo.HostSystem{}
	err = host.Properties(context.TODO(), host.Reference(), nil, hostProperties)
	Expect(err).To(BeNil())
	// simulator hosts don't have a DateTimeInfo so set one
	hostProperties.Config.DateTimeInfo = &types.HostDateTimeInfo{
		DynamicData: types.DynamicData{},
		TimeZone: types.HostDateTimeSystemTimeZone{
			GmtOffset: gmtOffsetSeconds,
		},
	}

	return vm, vmProperties, hostProperties
}

func prepareCredentials(server *simulator.Server) *mapper.DataVolumeCredentials {
	username := server.URL.User.Username()
	password, _ := server.URL.User.Password()
	return &mapper.DataVolumeCredentials{
		URL:        server.URL.String(),
		Username:   username,
		Password:   password,
		Thumbprint: "",
		SecretName: "",
	}
}

var _ = Describe("Test mapping virtual machine attributes", func() {
	var (
		vm             *object.VirtualMachine
		vmProperties   *mo.VirtualMachine
		hostProperties *mo.HostSystem
		credentials    *mapper.DataVolumeCredentials
	)

	BeforeEach(func() {
		model := simulator.VPX()
		err := model.Load("../../../../tests/vmware/vcsim")
		Expect(err).To(BeNil())

		server := model.Service.NewServer()
		client, _ := govmomi.NewClient(context.TODO(), server.URL, false)

		findOs = func() (string, error) {
			return "linux", nil
		}

		vm, vmProperties, hostProperties = prepareVsphereObjects(client)
		credentials = prepareCredentials(server)
	})

	It("should map name", func() {
		mappings := createMinimalMapping()
		vmMapper := mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, mappings, instanceUID, "", osFinder)
		vmSpec, err := vmMapper.MapVM(&targetVMName, &kubevirtv1.VirtualMachine{})
		Expect(err).To(BeNil())

		Expect(vmSpec.Name).To(Equal(vmProperties.Config.Name))
	})

	It("should map memory reservation", func() {
		mappings := createMinimalMapping()
		vmMapper := mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, mappings, instanceUID, "", osFinder)
		vmSpec, err := vmMapper.MapVM(&targetVMName, &kubevirtv1.VirtualMachine{})
		Expect(err).To(BeNil())

		quantity, _ := resource.ParseQuantity(memoryReservationStr)
		Expect(vmSpec.Spec.Template.Spec.Domain.Resources.Requests.Memory().Value()).To(Equal(quantity.Value()))
	})

	It("should map machine type", func() {
		mappings := createMinimalMapping()
		vmMapper := mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, mappings, instanceUID, "", osFinder)
		vmSpec, err := vmMapper.MapVM(&targetVMName, &kubevirtv1.VirtualMachine{})
		Expect(err).To(BeNil())

		Expect(vmSpec.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineType))
	})

	It("should map CPU topology", func() {
		mappings := createMinimalMapping()
		vmMapper := mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, mappings, instanceUID, "", osFinder)
		vmSpec, err := vmMapper.MapVM(&targetVMName, &kubevirtv1.VirtualMachine{})
		Expect(err).To(BeNil())

		Expect(int32(vmSpec.Spec.Template.Spec.Domain.CPU.Cores)).To(Equal(cpuCores))
		Expect(int32(vmSpec.Spec.Template.Spec.Domain.CPU.Sockets)).To(Equal(cpuSockets))

	})

	It("should map timezone", func() {
		mappings := createMinimalMapping()
		vmMapper := mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, mappings, instanceUID, "", osFinder)
		vmSpec, err := vmMapper.MapVM(&targetVMName, &kubevirtv1.VirtualMachine{})
		Expect(err).To(BeNil())

		Expect(int32(*vmSpec.Spec.Template.Spec.Domain.Clock.UTC.OffsetSeconds)).To(Equal(gmtOffsetSeconds))
	})

	It("should map pod network by moref", func() {
		mappings := createPodNetworkMapping(true)
		vmMapper := mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, mappings, instanceUID, "", osFinder)
		vmSpec, err := vmMapper.MapVM(&targetVMName, &kubevirtv1.VirtualMachine{})
		Expect(err).To(BeNil())

		interfaces := vmSpec.Spec.Template.Spec.Domain.Devices.Interfaces
		networks := vmSpec.Spec.Template.Spec.Networks
		networkInterfaceMultiQueue := vmSpec.Spec.Template.Spec.Domain.Devices.NetworkInterfaceMultiQueue

		// interface to be connected to a pod network
		Expect(interfaces[0].Name).To(Equal(networkNormalizedName))
		Expect(interfaces[0].Bridge).To(BeNil())
		Expect(interfaces[0].Masquerade).To(Not(BeNil()))
		Expect(interfaces[0].MacAddress).To(Equal(macAddress))
		Expect(networks[0].Name).To(Equal(networkNormalizedName))
		Expect(networks[0].Pod).To(Not(BeNil()))
		Expect(networkInterfaceMultiQueue).ToNot(BeNil())
		Expect(*networkInterfaceMultiQueue).To(BeTrue())
	})

	It("should map pod network by name", func() {
		mappings := createPodNetworkMapping(false)
		vmMapper := mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, mappings, instanceUID, "", osFinder)
		vmSpec, err := vmMapper.MapVM(&targetVMName, &kubevirtv1.VirtualMachine{})
		Expect(err).To(BeNil())

		interfaces := vmSpec.Spec.Template.Spec.Domain.Devices.Interfaces
		networks := vmSpec.Spec.Template.Spec.Networks
		networkInterfaceMultiQueue := vmSpec.Spec.Template.Spec.Domain.Devices.NetworkInterfaceMultiQueue

		// interface to be connected to a pod network
		Expect(interfaces[0].Name).To(Equal(networkNormalizedName))
		Expect(interfaces[0].Bridge).To(BeNil())
		Expect(interfaces[0].Masquerade).To(Not(BeNil()))
		Expect(interfaces[0].MacAddress).To(Equal(macAddress))
		Expect(networks[0].Name).To(Equal(networkNormalizedName))
		Expect(networks[0].Pod).To(Not(BeNil()))
		Expect(networkInterfaceMultiQueue).ToNot(BeNil())
		Expect(*networkInterfaceMultiQueue).To(BeTrue())
	})

	It("should map multus network by network moref", func() {
		mappings := createMultusNetworkMapping(true)
		vmMapper := mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, mappings, instanceUID, "", osFinder)
		vmSpec, err := vmMapper.MapVM(&targetVMName, &kubevirtv1.VirtualMachine{})
		Expect(err).To(BeNil())

		interfaces := vmSpec.Spec.Template.Spec.Domain.Devices.Interfaces
		networks := vmSpec.Spec.Template.Spec.Networks
		networkMapping := *mappings.NetworkMappings
		networkInterfaceMultiQueue := vmSpec.Spec.Template.Spec.Domain.Devices.NetworkInterfaceMultiQueue

		// interface to be connected to a multus network
		Expect(interfaces[0].Name).To(Equal(networkNormalizedName))
		Expect(interfaces[0].Bridge).To(Not(BeNil()))
		Expect(interfaces[0].MacAddress).To(Equal(macAddress))
		Expect(networks[0].Name).To(Equal(networkNormalizedName))
		Expect(networks[0].Multus.NetworkName).To(Equal(networkMapping[0].Target.Name))
		Expect(networkInterfaceMultiQueue).ToNot(BeNil())
		Expect(*networkInterfaceMultiQueue).To(BeTrue())
	})

	It("should map multus network by name", func() {
		mappings := createMultusNetworkMapping(false)
		vmMapper := mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, mappings, instanceUID, "", osFinder)
		vmSpec, err := vmMapper.MapVM(&targetVMName, &kubevirtv1.VirtualMachine{})
		Expect(err).To(BeNil())

		interfaces := vmSpec.Spec.Template.Spec.Domain.Devices.Interfaces
		networks := vmSpec.Spec.Template.Spec.Networks
		networkMapping := *mappings.NetworkMappings
		networkInterfaceMultiQueue := vmSpec.Spec.Template.Spec.Domain.Devices.NetworkInterfaceMultiQueue

		// interface to be connected to a multus network
		Expect(interfaces[0].Name).To(Equal(networkNormalizedName))
		Expect(interfaces[0].Bridge).To(Not(BeNil()))
		Expect(interfaces[0].MacAddress).To(Equal(macAddress))
		Expect(networks[0].Name).To(Equal(networkNormalizedName))
		Expect(networks[0].Multus.NetworkName).To(Equal(networkMapping[0].Target.Name))
		Expect(networkInterfaceMultiQueue).ToNot(BeNil())
		Expect(*networkInterfaceMultiQueue).To(BeTrue())
	})

	It("should disable NetworkInterfaceMultiQueue when there are no mapped interfaces", func() {
		mappings := createMinimalMapping()
		vmMapper := mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, mappings, instanceUID, "", osFinder)
		vmSpec, err := vmMapper.MapVM(&targetVMName, &kubevirtv1.VirtualMachine{})
		Expect(err).To(BeNil())

		interfaces := vmSpec.Spec.Template.Spec.Domain.Devices.Interfaces
		networks := vmSpec.Spec.Template.Spec.Networks
		networkInterfaceMultiQueue := vmSpec.Spec.Template.Spec.Domain.Devices.NetworkInterfaceMultiQueue

		Expect(len(interfaces)).To(Equal(0))
		Expect(len(networks)).To(Equal(0))
		Expect(networkInterfaceMultiQueue).ToNot(BeNil())
		Expect(*networkInterfaceMultiQueue).To(BeFalse())
	})

	It("should remove any networks or interfaces from the template", func() {
		mappings := &v1beta1.VmwareMappings{}
		vmMapper := mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, mappings, instanceUID, "", osFinder)
		vmSpec, err := vmMapper.MapVM(&targetVMName, &kubevirtv1.VirtualMachine{
			Spec: kubevirtv1.VirtualMachineSpec{
				Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
					Spec: kubevirtv1.VirtualMachineInstanceSpec{
						Domain: kubevirtv1.DomainSpec{
							Devices: kubevirtv1.Devices{
								Interfaces: []kubevirtv1.Interface{{}},
							},
						},
						Networks: []kubevirtv1.Network{{}},
					},
				},
			},
		})
		Expect(err).To(BeNil())
		Expect(len(vmSpec.Spec.Template.Spec.Networks)).To(Equal(0))
		Expect(len(vmSpec.Spec.Template.Spec.Domain.Devices.Interfaces)).To(Equal(0))
	})
})

var _ = Describe("Test mapping disks", func() {
	var (
		vm                 *object.VirtualMachine
		vmProperties       *mo.VirtualMachine
		hostProperties     *mo.HostSystem
		credentials        *mapper.DataVolumeCredentials
		storageClass       = "mystorageclass"
		filesystemOverhead = cdiv1.FilesystemOverhead{
			Global: "0.0",
			StorageClass: map[string]cdiv1.Percent{
				storageClass: "0.055",
			},
		}
	)

	BeforeEach(func() {
		model := simulator.VPX()
		err := model.Load("../../../../tests/vmware/vcsim")
		Expect(err).To(BeNil())
		server := model.Service.NewServer()
		client, _ := govmomi.NewClient(context.TODO(), server.URL, false)
		vm, vmProperties, hostProperties = prepareVsphereObjects(client)
		credentials = prepareCredentials(server)
	})

	It("should map datavolumes", func() {
		mappings := createMinimalMapping()
		mappings.DiskMappings = &[]v1beta1.StorageResourceMappingItem{
			{
				Source: v1beta1.Source{
					Name: &diskName1,
				},
				Target: v1beta1.ObjectIdentifier{
					Name: storageClass,
				},
				VolumeMode: &volumeModeBlock,
				AccessMode: &accessModeRWM,
			},
			{
				// using defaults
				Source: v1beta1.Source{
					Name: &diskName2,
				},
			},
		}
		mapper := mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, mappings, instanceUID, "", osFinder)
		dvs, err := mapper.MapDataVolumes(&targetVMName, filesystemOverhead)
		Expect(err).To(BeNil())
		Expect(dvs).To(HaveLen(expectedNumDisks))
		Expect(dvs).To(HaveKey(expectedDiskName1))
		Expect(dvs).To(HaveKey(expectedDiskName2))
		// check that mapped options are set correctly
		Expect(dvs[expectedDiskName1].Spec.PVC.VolumeMode).To(Equal(&volumeModeBlock))
		Expect(dvs[expectedDiskName1].Spec.PVC.AccessModes[0]).To(Equal(accessModeRWM))
		Expect(dvs[expectedDiskName1].Spec.PVC.StorageClassName).To(Equal(&storageClass))

		// check that defaults are set correctly
		Expect(dvs[expectedDiskName2].Spec.PVC.VolumeMode).To(Equal(&volumeModeFilesystem))
		Expect(dvs[expectedDiskName2].Spec.PVC.AccessModes[0]).To(Equal(accessModeRWO))
		Expect(dvs[expectedDiskName2].Spec.PVC.StorageClassName).To(BeNil())

		// check that disk overheads are set correctly
		Expect(dvs[expectedDiskName1].Spec.PVC.Resources.Requests).To(HaveKey(v1.ResourceStorage))
		storageResource := dvs[expectedDiskName1].Spec.PVC.Resources.Requests[v1.ResourceStorage]
		Expect(storageResource.Value()).To(BeEquivalentTo(diskOverhead1))

		Expect(dvs[expectedDiskName2].Spec.PVC.Resources.Requests).To(HaveKey(v1.ResourceStorage))
		storageResource = dvs[expectedDiskName2].Spec.PVC.Resources.Requests[v1.ResourceStorage]
		Expect(storageResource.Value()).To(BeEquivalentTo(diskBytes2))
	})
})

func createMinimalMapping() *v1beta1.VmwareMappings {
	return &v1beta1.VmwareMappings{
		NetworkMappings: &[]v1beta1.NetworkResourceMappingItem{},
		DiskMappings:    &[]v1beta1.StorageResourceMappingItem{},
	}
}

func createMultusNetworkMapping(byMoRef bool) *v1beta1.VmwareMappings {
	var networks []v1beta1.NetworkResourceMappingItem

	source := v1beta1.Source{}
	if byMoRef {
		source.ID = &networkMoRef
	} else {
		source.Name = &networkName
	}

	networks = append(networks,
		v1beta1.NetworkResourceMappingItem{
			Source: source,
			Target: v1beta1.ObjectIdentifier{
				Name: "net-attach-def",
			},
			Type: &multusNetwork,
		})

	return &v1beta1.VmwareMappings{
		NetworkMappings: &networks,
		DiskMappings:    &[]v1beta1.StorageResourceMappingItem{},
	}

}

func createPodNetworkMapping(byMoRef bool) *v1beta1.VmwareMappings {
	source := v1beta1.Source{}
	if byMoRef {
		source.ID = &networkMoRef
	} else {
		source.Name = &networkName
	}

	var networks []v1beta1.NetworkResourceMappingItem
	networks = append(networks,
		v1beta1.NetworkResourceMappingItem{
			Source: source,
			Type:   &podNetwork,
		})

	return &v1beta1.VmwareMappings{
		NetworkMappings: &networks,
		DiskMappings:    &[]v1beta1.StorageResourceMappingItem{},
	}
}
