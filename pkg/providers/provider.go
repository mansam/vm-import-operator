package provider

import (
	v2vv1alpha1 "github.com/kubevirt/vm-import-operator/pkg/apis/v2v/v1alpha1"
	oapiv1 "github.com/openshift/api/template/v1"
	corev1 "k8s.io/api/core/v1"
	kubevirtv1 "kubevirt.io/client-go/api/v1"
	cdiv1 "kubevirt.io/containerized-data-importer/pkg/apis/core/v1alpha1"
	rclient "sigs.k8s.io/controller-runtime/pkg/client"
	"k8s.io/apimachinery/pkg/types"
)

const (
	// VMStatusDown defines VM status representing stopped VM
	VMStatusDown VMStatus = "down"
	// VMStatusUp defines VM status representing running VM
	VMStatusUp VMStatus = "up"
)

// Provider defines the methods required by source providers for importing a VM
type Provider interface {
	Init(*corev1.Secret, *v2vv1alpha1.VirtualMachineImport) error
	TestConnection() error
	Close()
	LoadVM(v2vv1alpha1.VirtualMachineImportSourceSpec) error
	PrepareResourceMapping(*v2vv1alpha1.ResourceMappingSpec, v2vv1alpha1.VirtualMachineImportSourceSpec)
	Validate() ([]v2vv1alpha1.VirtualMachineImportCondition, error)
	ValidateDiskStatus(string) (bool, error)
	StopVM(*v2vv1alpha1.VirtualMachineImport, rclient.Client) error
	CreateMapper() (Mapper, error)
	GetVMStatus() (VMStatus, error)
	GetVMName() (string, error)
	StartVM() error
	CleanUp(bool, *v2vv1alpha1.VirtualMachineImport, rclient.Client) error
	FindTemplate() (*oapiv1.Template, error)
	ProcessTemplate(*oapiv1.Template, *string, string) (*kubevirtv1.VirtualMachine, error)
}

// Mapper is interface to be used for mapping external VM to kubevirt VM
type Mapper interface {
	CreateEmptyVM(vmName *string) *kubevirtv1.VirtualMachine
	ResolveVMName(targetVMName *string) *string
	MapVM(targetVMName *string, vmSpec *kubevirtv1.VirtualMachine) (*kubevirtv1.VirtualMachine, error)
	MapDataVolumes(targetVMName *string) (map[string]cdiv1.DataVolume, error)
	MapDisk(vmSpec *kubevirtv1.VirtualMachine, dv cdiv1.DataVolume)
}

// VMStatus represents VM status
type VMStatus string

// SecretsManager defines operations on secrets
type SecretsManager interface {
	FindFor(types.NamespacedName) (*corev1.Secret, error)
	CreateFor(*corev1.Secret, types.NamespacedName) error
	DeleteFor(types.NamespacedName) error
}

// ConfigMapsManager defines operations on config maps
type ConfigMapsManager interface {
	FindFor(types.NamespacedName) (*corev1.ConfigMap, error)
	CreateFor(*corev1.ConfigMap, types.NamespacedName) error
	DeleteFor(types.NamespacedName) error
}

// DataVolumesManager defines operations on datavolumes
type DataVolumesManager interface {
	FindFor(types.NamespacedName) ([]*cdiv1.DataVolume, error)
	DeleteFor(types.NamespacedName) error
}

// VirtualMachineManager defines operations on datavolumes
type VirtualMachineManager interface {
	FindFor(types.NamespacedName) (*kubevirtv1.VirtualMachine, error)
	DeleteFor(types.NamespacedName) error
}
