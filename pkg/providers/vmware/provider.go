package vmware

import (
	"context"
	v2vv1alpha1 "github.com/kubevirt/vm-import-operator/pkg/apis/v2v/v1alpha1"
	pclient "github.com/kubevirt/vm-import-operator/pkg/client"
	"github.com/kubevirt/vm-import-operator/pkg/config"
	"github.com/kubevirt/vm-import-operator/pkg/configmaps"
	"github.com/kubevirt/vm-import-operator/pkg/datavolumes"
	"github.com/kubevirt/vm-import-operator/pkg/os"
	provider "github.com/kubevirt/vm-import-operator/pkg/providers"
	"github.com/kubevirt/vm-import-operator/pkg/providers/vmware/mapper"
	"github.com/kubevirt/vm-import-operator/pkg/providers/vmware/mappings"
	vos "github.com/kubevirt/vm-import-operator/pkg/providers/vmware/os"
	vtemplates "github.com/kubevirt/vm-import-operator/pkg/providers/vmware/templates"
	//"github.com/kubevirt/vm-import-operator/pkg/providers/vmware/validation"
	//"github.com/kubevirt/vm-import-operator/pkg/providers/vmware/validation/validators"
	"github.com/kubevirt/vm-import-operator/pkg/secrets"
	"github.com/kubevirt/vm-import-operator/pkg/templates"
	"github.com/kubevirt/vm-import-operator/pkg/virtualmachines"
	oapiv1 "github.com/openshift/api/template/v1"
	tempclient "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "kubevirt.io/client-go/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	vmwareSecretKey = "vmware"
)

type VmwareProvider struct {
	vmiObjectMeta         metav1.ObjectMeta
	vmiTypeMeta           metav1.TypeMeta
	vmwareClient          pclient.VMClient
	factory               pclient.Factory
	vmwareSecretDataMap   map[string]string
	instance              *v2vv1alpha1.VirtualMachineImport
	templateHandler       *templates.TemplateHandler
	secretsManager        provider.SecretsManager
	configMapsManager     provider.ConfigMapsManager
	datavolumesManager    provider.DataVolumesManager
	virtualMachineManager provider.VirtualMachineManager
	//validator             validation.VirtualMachineImportValidator
	vm                    *object.VirtualMachine
	resourceMapping       *v2vv1alpha1.VmwareMappings
	templateFinder        *vtemplates.TemplateFinder
	osFinder              *vos.VmwareOSFinder
	vmProperties          *mo.VirtualMachine
}

func (r *VmwareProvider) ProcessTemplate(template *oapiv1.Template, s *string, s2 string) (*v1.VirtualMachine, error) {
	panic("implement me")
}

func (r *VmwareProvider) getClient() (pclient.VMClient, error) {
	if r.vmwareClient == nil {
		c, err := r.factory.NewVmwareClient(r.vmwareSecretDataMap)
		if err != nil {
			return nil, err
		}
		r.vmwareClient = c
	}
	return r.vmwareClient, nil
}

func NewVmwareProvider(vmiObjectMeta metav1.ObjectMeta, vmiTypeMeta metav1.TypeMeta, client client.Client, tempClient *tempclient.TemplateV1Client, factory pclient.Factory,
	kvConfigProvider config.KubeVirtConfigProvider) VmwareProvider {
	//validator             := validators.NewValidatorWrapper(client, kvConfigProvider)
	secretsManager        := secrets.NewManager(client)
	configMapsManager     := configmaps.NewManager(client)
	datavolumesManager    := datavolumes.NewManager(client)
	virtualMachineManager := virtualmachines.NewManager(client)
	templateProvider      := templates.NewTemplateProvider(tempClient)
	osFinder              := vos.VmwareOSFinder{OsMapProvider: os.NewOSMapProvider(client)}
	return VmwareProvider{
		vmiObjectMeta:         vmiObjectMeta,
		vmiTypeMeta:           vmiTypeMeta,
		factory:               factory,
		secretsManager:        &secretsManager,
		configMapsManager:     &configMapsManager,
		datavolumesManager:    &datavolumesManager,
		virtualMachineManager: &virtualMachineManager,
		osFinder:              &osFinder,
		//validator:             validator,
		templateHandler:       templates.NewTemplateHandler(templateProvider),
		templateFinder:        vtemplates.NewTemplateFinder(templateProvider, osFinder),

	}
}

func (r *VmwareProvider) Init(secret *corev1.Secret, instance *v2vv1alpha1.VirtualMachineImport) error {
	r.vmwareSecretDataMap = make(map[string]string)
	err := yaml.Unmarshal(secret.Data[vmwareSecretKey], &r.vmwareSecretDataMap)
	if err != nil {
		return err
	}
	r.instance = instance
	return nil
}

func (r *VmwareProvider) Close() {}

func (r *VmwareProvider) LoadVM(sourceSpec v2vv1alpha1.VirtualMachineImportSourceSpec) error {
	client, err := r.getClient()
	if err != nil {
		return err
	}
	vm, err := client.GetVM(sourceSpec.Vmware.VM.ID, nil, nil, nil)
	if err != nil {
		return err
	}
	r.vm = vm.(*object.VirtualMachine)
	return nil
}

func (r *VmwareProvider) getVM() (*object.VirtualMachine, error) {
	if r.vm == nil {
		err := r.LoadVM(r.instance.Spec.Source)
		if err != nil {
			return nil, err
		}
	}
	return r.vm, nil
}

func (r *VmwareProvider) getVmProperties() (*mo.VirtualMachine, error) {
	if r.vmProperties == nil {
		vmProperties := mo.VirtualMachine{}
		err := r.vm.Properties(context.TODO(), r.vm.Reference(), nil, vmProperties)
		if err != nil {
			return nil, err
		}
		r.vmProperties = &vmProperties
	}
	return r.vmProperties, nil
}

func (r *VmwareProvider) PrepareResourceMapping(externalResourceMapping *v2vv1alpha1.ResourceMappingSpec, vmiSpec v2vv1alpha1.VirtualMachineImportSourceSpec) {
	r.resourceMapping = mappings.MergeMappings(externalResourceMapping, vmiSpec.Vmware.Mappings)
}

func (r *VmwareProvider) Validate() ([]v2vv1alpha1.VirtualMachineImportCondition, error) {
	return nil, nil
}

func (r *VmwareProvider) StopVM() error {
	vm, err := r.getVM()
	if err != nil {
		return err
	}
	task, err := vm.PowerOff(context.TODO())
	if err != nil {
		return err
	}
	return task.Wait(context.TODO())
}

func (r *VmwareProvider) CreateMapper() (provider.Mapper, error) {
	vm, err := r.getVM()
	if err != nil {
		return nil, err
	}
	vmProperties, err := r.getVmProperties()
	if err != nil {
		return nil, err
	}
	return mapper.NewVmwareMapper(vm, vmProperties, r.resourceMapping, r.vmiObjectMeta.Namespace), nil
}

func (r *VmwareProvider) GetVMStatus() (provider.VMStatus, error) {
	vmProperties, err := r.getVmProperties()
	if err != nil {
		return "", err
	}
	if vmProperties.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn {
		return provider.VMStatusUp, nil
	}
	return provider.VMStatusDown, nil
}

func (r *VmwareProvider) GetVMName() (string, error) {
	vm, err := r.getVM()
	if err != nil {
		return "", err
	}
	return vm.Name(), nil
}

func (r *VmwareProvider) StartVM() error {
	vm, err := r.getVM()
	if err != nil {
		return err
	}
	task, err := vm.PowerOn(context.TODO())
	if err != nil {
		return err
	}
	return task.Wait(context.TODO())
}

func (r *VmwareProvider) CleanUp(bool) error {
	return nil
}

func (r *VmwareProvider) FindTemplate() (*oapiv1.Template, error) {
	vm, err := r.getVmProperties()
	if err != nil {
		return nil, err
	}
	return r.templateFinder.FindTemplate(vm)
}