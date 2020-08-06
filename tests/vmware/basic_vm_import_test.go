package vmware_test

import (
	//"context"
	//"github.com/vmware/govmomi"
	"time"
	"fmt"
	v2vv1alpha1 "github.com/kubevirt/vm-import-operator/pkg/apis/v2v/v1alpha1"
	"github.com/onsi/ginkgo/extensions/table"

	fwk "github.com/kubevirt/vm-import-operator/tests/framework"
	. "github.com/kubevirt/vm-import-operator/tests/matchers"
	"github.com/kubevirt/vm-import-operator/tests/ovirt/vms"
	"github.com/kubevirt/vm-import-operator/tests/utils"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "kubevirt.io/client-go/api/v1"
)

type basicVmImportTest struct {
	framework *fwk.Framework
}

var _ = Describe("Basic VM import ", func() {

	var (
		f         = fwk.NewFrameworkOrDie("basic-vm-import")
		test      = basicVmImportTest{framework: f}
		secret    corev1.Secret
		namespace string
		vmID      = "vm-2782"
	)

	BeforeEach(func() {
		namespace = f.Namespace.Name

		//model := simulator.VPX()
		//err := model.Load("sim")
		//Expect(err).To(BeNil())
		var err error
		secret, err = f.CreateVmwareSecretInNamespace(namespace)
		if err != nil {
			Fail("Cannot create secret: " + err.Error())
		}
	})

	Context(" without resource mapping", func() {
		It("should create stopped VM", func() {
			vmi := utils.VirtualMachineImportCr(fwk.ProviderVmware, vmID, namespace, secret.Name, f.NsPrefix, false)
			fmt.Printf("%v", vmi.Spec.Source.Vmware.VM.ID)

			created, err := f.VMImportClient.V2vV1alpha1().VirtualMachineImports(namespace).Create(&vmi)

			Expect(err).NotTo(HaveOccurred())
			Expect(created).To(BeSuccessful(f))

			retrieved, _ := f.VMImportClient.V2vV1alpha1().VirtualMachineImports(namespace).Get(created.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			vmBlueprint := v1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Name: retrieved.Status.TargetVMName, Namespace: namespace}}
			Expect(vmBlueprint).NotTo(BeRunning(f).Timeout(2 * time.Minute))

			vm := test.validateTargetConfiguration(vmBlueprint.Name)
			Expect(vm.Spec.Template.Spec.Volumes[0].DataVolume.Name).To(HaveDefaultStorageClass(f))
		})

		It("should create started VM", func() {
			vmi := utils.VirtualMachineImportCr(fwk.ProviderVmware, vmID, namespace, secret.Name, f.NsPrefix, true)
			created, err := f.VMImportClient.V2vV1alpha1().VirtualMachineImports(namespace).Create(&vmi)

			Expect(err).NotTo(HaveOccurred())
			Expect(created).To(BeSuccessful(f))

			retrieved, _ := f.VMImportClient.V2vV1alpha1().VirtualMachineImports(namespace).Get(created.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			vmBlueprint := v1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Name: retrieved.Status.TargetVMName, Namespace: namespace}}
			Expect(vmBlueprint).To(BeRunning(f))

			vm := test.validateTargetConfiguration(vmBlueprint.Name)
			Expect(vm.Spec.Template.Spec.Volumes[0].DataVolume.Name).To(HaveDefaultStorageClass(f))
		})
	})

	Context(" with in-CR resource mapping", func() {
		table.DescribeTable("should create running VM", func(mappings v2vv1alpha1.VmwareMappings, storageClass string) {
			vmi := utils.VirtualMachineImportCr(fwk.ProviderVmware, vmID, namespace, secret.Name, f.NsPrefix, true)
			vmi.Spec.Source.Vmware.Mappings = &mappings

			created, err := f.VMImportClient.V2vV1alpha1().VirtualMachineImports(namespace).Create(&vmi)

			Expect(err).NotTo(HaveOccurred())
			Expect(created).To(BeSuccessful(f))

			retrieved, _ := f.VMImportClient.V2vV1alpha1().VirtualMachineImports(namespace).Get(created.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			vmBlueprint := v1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Name: retrieved.Status.TargetVMName, Namespace: namespace}}
			Expect(vmBlueprint).To(BeRunning(f))

			vm := test.validateTargetConfiguration(vmBlueprint.Name)
			Expect(vm.Spec.Template.Spec.Volumes[0].DataVolume.Name).To(HaveStorageClass(storageClass, f))
		},
			table.Entry(" for disk", v2vv1alpha1.VmwareMappings{
				DiskMappings: &[]v2vv1alpha1.ResourceMappingItem{
					{Source: v2vv1alpha1.Source{ID: &vms.DiskID}, Target: v2vv1alpha1.ObjectIdentifier{Name: f.DefaultStorageClass}},
				},
			}, f.DefaultStorageClass),
			table.Entry(" for storage domain", v2vv1alpha1.VmwareMappings{
				StorageMappings: &[]v2vv1alpha1.ResourceMappingItem{
					{Source: v2vv1alpha1.Source{ID: &vms.StorageDomainID}, Target: v2vv1alpha1.ObjectIdentifier{Name: f.DefaultStorageClass}},
				},
			}, f.DefaultStorageClass))
	})

	Context("when it's successful and the import CR is removed", func() {
		It("should not affect imported VM or VMI", func() {
			By("Creating Virtual Machine Import")
			vmi := utils.VirtualMachineImportCr(fwk.ProviderVmware, vmID, namespace, secret.Name, f.NsPrefix, true)
			vmImports := f.VMImportClient.V2vV1alpha1().VirtualMachineImports(namespace)

			created, err := vmImports.Create(&vmi)

			Expect(err).NotTo(HaveOccurred())
			Expect(created).To(BeSuccessful(f))

			retrieved, _ := vmImports.Get(created.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			vmBlueprint := v1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Name: retrieved.Status.TargetVMName, Namespace: namespace}}
			By("Making sure the imported VM is running")
			Expect(vmBlueprint).To(BeRunning(f))

			When("VM import CR is deleted", func() {
				foreground := metav1.DeletePropagationForeground
				deleteOptions := metav1.DeleteOptions{
					PropagationPolicy: &foreground,
				}
				err = vmImports.Delete(retrieved.Name, &deleteOptions)
				if err != nil {
					Fail(err.Error())
				}

				By("Waiting for VM import removal")
				err = f.EnsureVMImportDoesNotExist(retrieved.Name)
				Expect(err).ToNot(HaveOccurred())
			})

			Consistently(func() (*v1.VirtualMachineInstance, error) {
				vmi, err := f.KubeVirtClient.VirtualMachineInstance(namespace).Get(vmBlueprint.Name, &metav1.GetOptions{})
				if err != nil {
					return nil, err
				}
				return vmi, nil

			}, 2*time.Minute, 15*time.Second).ShouldNot(BeNil())
			vmInstance, err := f.KubeVirtClient.VirtualMachineInstance(namespace).Get(vmBlueprint.Name, &metav1.GetOptions{})
			if err != nil {
				Fail(err.Error())
			}
			Expect(vmInstance.IsRunning()).To(BeTrue())

			vm, err := f.KubeVirtClient.VirtualMachine(namespace).Get(vmBlueprint.Name, &metav1.GetOptions{})
			if err != nil {
				Fail(err.Error())
			}
			Expect(vm).NotTo(BeNil())
		})
	})
})

func (t *basicVmImportTest) validateTargetConfiguration(vmName string) *v1.VirtualMachine {
	vmNamespace := t.framework.Namespace.Name
	f := t.framework
	vm, _ := f.KubeVirtClient.VirtualMachine(vmNamespace).Get(vmName, &metav1.GetOptions{})
	spec := vm.Spec.Template.Spec

	By("having correct machine type")
	Expect(spec.Domain.Machine.Type).To(BeEquivalentTo("q35"))

	By("having BIOS")
	Expect(spec.Domain.Firmware.Bootloader.BIOS).NotTo(BeNil())
	Expect(spec.Domain.Firmware.Bootloader.EFI).To(BeNil())

	By("having correct CPU configuration")
	cpu := spec.Domain.CPU
	Expect(cpu.Cores).To(BeEquivalentTo(1))
	Expect(cpu.Sockets).To(BeEquivalentTo(1))
	Expect(cpu.Threads).To(BeEquivalentTo(1))

	By("having no network")
	Expect(spec.Networks).To(BeEmpty())
	Expect(spec.Domain.Devices.Interfaces).To(BeEmpty())

	By("having correct clock settings")
	Expect(spec.Domain.Clock.UTC).ToNot(BeNil())

	By("having correct disk setup")
	disks := spec.Domain.Devices.Disks
	Expect(disks).To(HaveLen(1))
	disk1 := disks[0]
	Expect(disk1.Disk.Bus).To(BeEquivalentTo("virtio"))
	Expect(*disk1.BootOrder).To(BeEquivalentTo(1))

	By("having correct volumes")
	Expect(spec.Volumes).To(HaveLen(1))

	By("having correct VNC setup")
	Expect(*spec.Domain.Devices.AutoattachGraphicsDevice).To(BeTrue())

	inputDevices := spec.Domain.Devices.Inputs
	tablet := utils.FindTablet(inputDevices)
	Expect(tablet).NotTo(BeNil())
	Expect(tablet.Bus).To(BeEquivalentTo("virtio"))
	Expect(tablet.Type).To(BeEquivalentTo("tablet"))

	return vm
}
