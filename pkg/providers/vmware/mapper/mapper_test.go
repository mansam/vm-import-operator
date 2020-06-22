package mapper_test

import (
	"context"
	v2vv1alpha1 "github.com/kubevirt/vm-import-operator/pkg/apis/v2v/v1alpha1"
	"github.com/kubevirt/vm-import-operator/pkg/providers/vmware/mapper"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"k8s.io/apimachinery/pkg/api/resource"
	kubevirtv1 "kubevirt.io/client-go/api/v1"
)

var (
	targetVMName = "myvm"
	memoryReservationMi int64 = 1024
	memoryReservationStr = "1024Mi"
	cpuCores int32 = 4
	cpuSockets int32 = 2
	gmtOffsetSeconds int32 = 3600
)

var _ = Describe("Test mapping virtual machine attributes", func() {
	var (
		vm *object.VirtualMachine
		vmProperties *mo.VirtualMachine
		mappings v2vv1alpha1.VmwareMappings
		vmSpec *kubevirtv1.VirtualMachine

	)

	BeforeEach(func() {
		model := simulator.VPX()
		_ = model.Create()
		server := model.Service.NewServer()
		client, _ := govmomi.NewClient(context.TODO(), server.URL, false)

		simVm := simulator.Map.Any("VirtualMachine").(*simulator.VirtualMachine)
		simHost := simulator.Map.Get(simVm.Runtime.Host.Reference()).(*simulator.HostSystem)
		simHost.Config.DateTimeInfo = &types.HostDateTimeInfo{
			DynamicData:         types.DynamicData{},
			TimeZone:            types.HostDateTimeSystemTimeZone{
				GmtOffset: gmtOffsetSeconds,
			},
			SystemClockProtocol: "",
			NtpConfig:           nil,
		}
		simVm.Config.Name = targetVMName
		simVm.Config.Hardware.NumCPU = cpuSockets
		simVm.Config.Hardware.NumCoresPerSocket = cpuCores
		simVm.Guest.Net = []types.GuestNicInfo{
			{
				DynamicData:    types.DynamicData{},
				Network:        "network1",
				IpAddress:      nil,
				MacAddress:     "ABCABCABC1",
				Connected:      false,
				DeviceConfigId: 0,
				DnsConfig:      nil,
				IpConfig:       nil,
				NetBIOSConfig:  nil,
			},
			{
				DynamicData:    types.DynamicData{},
				Network:        "network2",
				IpAddress:      nil,
				MacAddress:     "ABCABCABC2",
				Connected:      false,
				DeviceConfigId: 0,
				DnsConfig:      nil,
				IpConfig:       nil,
				NetBIOSConfig:  nil,
			},
		}
		simVm.ResourceConfig = &types.ResourceConfigSpec{
			MemoryAllocation:       types.ResourceAllocationInfo{
				Reservation: &memoryReservationMi,
				Limit:       &memoryReservationMi,
			},
			ScaleDescendantsShares: "",
		}
		simulator.Map.Put(simVm)
		vm = object.NewVirtualMachine(client.Client, simVm.Reference())
		vmProperties = &mo.VirtualMachine{}
		err := vm.Properties(context.TODO(), vm.Reference(), nil, vmProperties)
		Expect(err).To(BeNil())
		mappings = createMappings()
		mapper := mapper.NewVmwareMapper(vm, vmProperties, &mappings, "")
		vmSpec, _ = mapper.MapVM(&targetVMName, &kubevirtv1.VirtualMachine{})
	})

	It("should map name", func() {
		Expect(vmSpec.Name).To(Equal(vmProperties.Config.Name))
	})

	It("should map memory reservation", func() {
		quantity, _ := resource.ParseQuantity(memoryReservationStr)
		Expect(vmSpec.Spec.Template.Spec.Domain.Resources.Requests.Memory().Value()).To(Equal(quantity.Value()))
	})

	It("should map CPU topology", func() {
		Expect(int32(vmSpec.Spec.Template.Spec.Domain.CPU.Cores)).To(Equal(cpuCores))
		Expect(int32(vmSpec.Spec.Template.Spec.Domain.CPU.Sockets)).To(Equal(cpuSockets))

	})

	It("should map timezone", func() {
		Expect(int32(*vmSpec.Spec.Template.Spec.Domain.Clock.UTC.OffsetSeconds)).To(Equal(gmtOffsetSeconds))
	})

	It("should map networks", func() {
		interfaces := vmSpec.Spec.Template.Spec.Domain.Devices.Interfaces
		networks := vmSpec.Spec.Template.Spec.Networks
		networkMapping := *mappings.NetworkMappings

		// interface to be connected to a multus network
		nic1 := vmProperties.Guest.Net[0]
		Expect(interfaces[0].Name).To(Equal(nic1.Network))
		Expect(interfaces[0].Bridge).To(Not(BeNil()))
		Expect(interfaces[0].MacAddress).To(Equal(nic1.MacAddress))
		Expect(networks[0].Name).To(Equal(nic1.Network))
		Expect(networks[0].Multus.NetworkName).To(Equal(networkMapping[0].Target.Name))

		// interface to be connected to a pod network
		nic2 := vmProperties.Guest.Net[1]
		Expect(interfaces[1].Name).To(Equal(nic2.Network))
		Expect(interfaces[1].Bridge).To(BeNil())
		Expect(interfaces[1].Masquerade).To(Not(BeNil()))
		Expect(interfaces[1].MacAddress).To(Equal(nic2.MacAddress))
		Expect(networks[1].Name).To(Equal(nic2.Network))
		Expect(networks[1].Pod).To(Not(BeNil()))
	})
})

var _ = Describe("Test mapping disks", func() {
	var (
		vm *object.VirtualMachine
		vmProperties *mo.VirtualMachine
	)

	BeforeEach(func() {
		model := simulator.VPX()
		_ = model.Create()
		server := model.Service.NewServer()
		client, _ := govmomi.NewClient(context.TODO(), server.URL, false)
		simVm := simulator.Map.Any("VirtualMachine").(*simulator.VirtualMachine)
		vm = object.NewVirtualMachine(client.Client, simVm.Reference())
		vmProperties = &mo.VirtualMachine{}
		err := vm.Properties(context.TODO(), vm.Reference(), nil, vmProperties)
		Expect(err).To(BeNil())
	})

	It("should map disk", func() {
		mappings := createMappings()
		namespace := "my-namespace"
		mapper := mapper.NewVmwareMapper(vm, vmProperties, &mappings, namespace)
		dvs, _ := mapper.MapDataVolumes(&targetVMName)
		Expect(dvs).To(HaveLen(1))
		Expect(dvs).To(HaveKey("myvm-disk-202-0"))
	})
})

func createMappings() v2vv1alpha1.VmwareMappings {
	multusNetwork := "multus"
	multusNetworkName := "network1"
	podNetwork := "pod"
	podNetworkName := "network2"

	var networks []v2vv1alpha1.ResourceMappingItem
	networks = append(networks,
		v2vv1alpha1.ResourceMappingItem{
			Source: v2vv1alpha1.Source{
				Name: &multusNetworkName,
			},
			Target: v2vv1alpha1.ObjectIdentifier{
				Name: "net-attach-def",
			},
			Type: &multusNetwork,
		},
		v2vv1alpha1.ResourceMappingItem{
			Source: v2vv1alpha1.Source{
				Name: &podNetworkName,
			},
			Type: &podNetwork,
		})

	return v2vv1alpha1.VmwareMappings{
		NetworkMappings: &networks,
		DiskMappings: &[]v2vv1alpha1.ResourceMappingItem{},
	}
}