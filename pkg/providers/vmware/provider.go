package vmware

import (
	v2vv1alpha1 "github.com/kubevirt/vm-import-operator/pkg/apis/v2v/v1alpha1"
	pclient "github.com/kubevirt/vm-import-operator/pkg/client"
	"github.com/kubevirt/vm-import-operator/pkg/config"
	"github.com/kubevirt/vm-import-operator/pkg/configmaps"
	"github.com/kubevirt/vm-import-operator/pkg/datavolumes"
	provider "github.com/kubevirt/vm-import-operator/pkg/providers"
	"github.com/kubevirt/vm-import-operator/pkg/providers/vmware/validation"
	"github.com/kubevirt/vm-import-operator/pkg/providers/vmware/validation/validators"
	"github.com/kubevirt/vm-import-operator/pkg/secrets"
	"github.com/kubevirt/vm-import-operator/pkg/templates"
	"github.com/kubevirt/vm-import-operator/pkg/virtualmachines"
	oapiv1 "github.com/openshift/api/template/v1"
	tempclient "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"
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
	validator             validation.VirtualMachineImportValidator

}

func (r *VmwareProvider) ProcessTemplate(template *oapiv1.Template, s *string, s2 string) (*v1.VirtualMachine, error) {
	panic("implement me")
}

func (r *VmwareProvider) getClient() (pclient.VMClient, error) {
	if r.vmwareClient == nil {
		client, err := r.factory.NewVmwareClient(r.vmwareSecretDataMap)
		if err != nil {
			return nil, err
		}
		r.vmwareClient = client
	}
	return r.vmwareClient, nil
}

func NewVmwareProvider(vmiObjectMeta metav1.ObjectMeta, vmiTypeMeta metav1.TypeMeta, client client.Client, tempClient *tempclient.TemplateV1Client, factory pclient.Factory,
	kvConfigProvider config.KubeVirtConfigProvider) VmwareProvider {
	validator             := validators.NewValidatorWrapper(client, kvConfigProvider)
	secretsManager        := secrets.NewManager(client)
	configMapsManager     := configmaps.NewManager(client)
	datavolumesManager    := datavolumes.NewManager(client)
	virtualMachineManager := virtualmachines.NewManager(client)
	templateProvider      := templates.NewTemplateProvider(tempClient)
	return VmwareProvider{
		vmiObjectMeta:         vmiObjectMeta,
		vmiTypeMeta:           vmiTypeMeta,
		factory:               factory,
		secretsManager:        &secretsManager,
		configMapsManager:     &configMapsManager,
		datavolumesManager:    &datavolumesManager,
		virtualMachineManager: &virtualMachineManager,
		validator:             validator,
		templateHandler:       templates.NewTemplateHandler(templateProvider),
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
	//vmwareSourceSpec := sourceSpec.Vmware
	//sourceId := vmwareSourceSpec.VM.ID
	return nil
}

func (r *VmwareProvider) PrepareResourceMapping(rmSpec *v2vv1alpha1.ResourceMappingSpec, vmiSpec v2vv1alpha1.VirtualMachineImportSourceSpec) {

}

func (r *VmwareProvider) Validate() ([]v2vv1alpha1.VirtualMachineImportCondition, error) {
	return nil, nil
}

func (r *VmwareProvider) StopVM() error {
	return nil
}

func (r *VmwareProvider) CreateMapper() (provider.Mapper, error) {
	return nil, nil
}

func (r *VmwareProvider) GetVMStatus() (provider.VMStatus, error) {
	return "", nil
}

func (r *VmwareProvider) GetVMName() (string, error) {
	return "", nil
}

func (r *VmwareProvider) StartVM() error {
	return nil
}

func (r *VmwareProvider) CleanUp(bool) error {
	return nil
}

func (r *VmwareProvider) FindTemplate() (*oapiv1.Template, error) {
	return nil, nil
}
