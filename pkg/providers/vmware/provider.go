package vmware

import (
	"fmt"
	v2vv1alpha1 "github.com/kubevirt/vm-import-operator/pkg/apis/v2v/v1alpha1"
	pclient "github.com/kubevirt/vm-import-operator/pkg/client"
	"github.com/kubevirt/vm-import-operator/pkg/config"
	"github.com/kubevirt/vm-import-operator/pkg/datavolumes"
	"github.com/kubevirt/vm-import-operator/pkg/os"
	"github.com/kubevirt/vm-import-operator/pkg/ownerreferences"
	provider "github.com/kubevirt/vm-import-operator/pkg/providers"
	vclient "github.com/kubevirt/vm-import-operator/pkg/providers/vmware/client"
	"github.com/kubevirt/vm-import-operator/pkg/providers/vmware/mapper"
	"github.com/kubevirt/vm-import-operator/pkg/providers/vmware/mappings"
	vos "github.com/kubevirt/vm-import-operator/pkg/providers/vmware/os"
	vtemplates "github.com/kubevirt/vm-import-operator/pkg/providers/vmware/templates"
	"github.com/kubevirt/vm-import-operator/pkg/secrets"
	"github.com/kubevirt/vm-import-operator/pkg/templates"
	"github.com/kubevirt/vm-import-operator/pkg/utils"
	"github.com/kubevirt/vm-import-operator/pkg/virtualmachines"
	oapiv1 "github.com/openshift/api/template/v1"
	tempclient "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	v1 "kubevirt.io/client-go/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	apiUrlKey       = "apiUrl"
	keyAccessKey    = "accessKeyId"
	keySecretKey    = "secretKey"
	thumbprintKey   = "thumbprint"
	vmwareSecretKey = "vmware"
)

type VmwareProvider struct {
	dataVolumesManager    provider.DataVolumesManager
	factory               pclient.Factory
	instance              *v2vv1alpha1.VirtualMachineImport
	osFinder              *vos.VmwareOSFinder
	resourceMapping       *v2vv1alpha1.VmwareMappings
	secretsManager        provider.SecretsManager
	templateFinder        *vtemplates.TemplateFinder
	templateHandler       *templates.TemplateHandler
	virtualMachineManager provider.VirtualMachineManager
	vm                    *object.VirtualMachine
	vmProperties          *mo.VirtualMachine
	vmiObjectMeta         metav1.ObjectMeta
	vmiTypeMeta           metav1.TypeMeta
	vmwareClient          *vclient.RichVmwareClient
	vmwareSecretDataMap   map[string]string
}

func (r *VmwareProvider) TestConnection() error {
	vmwareClient, err := r.getClient()
	if err != nil {
		return err
	}
	return vmwareClient.TestConnection()
}

func (r *VmwareProvider) ValidateDiskStatus(s string) (bool, error) {
	return true, nil
}

func NewVmwareProvider(vmiObjectMeta metav1.ObjectMeta, vmiTypeMeta metav1.TypeMeta, client client.Client, tempClient *tempclient.TemplateV1Client, factory pclient.Factory,
	_ config.KubeVirtConfigProvider) VmwareProvider {
	secretsManager := secrets.NewManager(client)
	dataVolumesManager := datavolumes.NewManager(client)
	virtualMachineManager := virtualmachines.NewManager(client)
	templateProvider := templates.NewTemplateProvider(tempClient)
	osFinder := vos.VmwareOSFinder{OsMapProvider: os.NewOSMapProvider(client)}
	return VmwareProvider{
		vmiObjectMeta:         vmiObjectMeta,
		vmiTypeMeta:           vmiTypeMeta,
		factory:               factory,
		secretsManager:        &secretsManager,
		dataVolumesManager:    &dataVolumesManager,
		virtualMachineManager: &virtualMachineManager,
		osFinder:              &osFinder,
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

func (r *VmwareProvider) FindTemplate() (*oapiv1.Template, error) {
	vm, err := r.getVmProperties()
	if err != nil {
		return nil, err
	}
	return r.templateFinder.FindTemplate(vm)
}

func (r *VmwareProvider) ProcessTemplate(template *oapiv1.Template, vmName *string, namespace string) (*v1.VirtualMachine, error) {
	vm, err := r.templateHandler.ProcessTemplate(template, vmName, namespace)
	if err != nil {
		return nil, err
	}
	vmProperties, err := r.getVmProperties()
	if err != nil {
		return nil, err
	}
	labels, annotations, err := r.templateFinder.GetMetadata(template, vmProperties)
	if err != nil {
		return nil, err
	}
	updateLabels(vm, labels)
	updateAnnotations(vm, annotations)
	return vm, nil
}

func (r *VmwareProvider) getClient() (*vclient.RichVmwareClient, error) {
	if r.vmwareClient == nil {
		c, err := r.factory.NewVmwareClient(r.vmwareSecretDataMap)
		if err != nil {
			return nil, err
		}
		r.vmwareClient = c.(*vclient.RichVmwareClient)
	}
	return r.vmwareClient, nil
}

func (r *VmwareProvider) CreateMapper() (provider.Mapper, error) {
	credentials, err := r.prepareDataVolumeCredentials()
	if err != nil {
		return nil, err
	}
	vmwareClient, err := r.getClient()
	if err != nil {
		return nil, err
	}
	vm, err := r.getVM()
	if err != nil {
		return nil, err
	}
	vmProperties, err := r.getVmProperties()
	if err != nil {
		return nil, err
	}
	hostProperties, err := vmwareClient.GetVMHostProperties(vm)
	if err != nil {
		return nil, err
	}
	return mapper.NewVmwareMapper(vm, vmProperties, hostProperties, credentials, r.resourceMapping, r.vmiObjectMeta.Namespace, r.osFinder), nil
}

func (r *VmwareProvider) PrepareResourceMapping(externalResourceMapping *v2vv1alpha1.ResourceMappingSpec, vmiSpec v2vv1alpha1.VirtualMachineImportSourceSpec) {
	r.resourceMapping = mappings.MergeMappings(externalResourceMapping, vmiSpec.Vmware.Mappings)
}

func (r *VmwareProvider) LoadVM(sourceSpec v2vv1alpha1.VirtualMachineImportSourceSpec) error {
	vmwareClient, err := r.getClient()
	if err != nil {
		return err
	}
	vm, err := vmwareClient.GetVM(sourceSpec.Vmware.VM.ID, sourceSpec.Vmware.VM.Name, nil, nil)
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
	vmwareClient, err := r.getClient()
	if err != nil {
		return nil, err
	}
	vm, err := r.getVM()
	if err != nil {
		return nil, err
	}
	if r.vmProperties == nil {
		properties, err := vmwareClient.GetVMProperties(vm)
		if err != nil {
			return nil, err
		}
		r.vmProperties = properties
	}
	return r.vmProperties, nil
}

func (r *VmwareProvider) GetVMName() (string, error) {
	vm, err := r.getVM()
	if err != nil {
		return "", err
	}
	return vm.Name(), nil
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

func (r *VmwareProvider) StartVM() error {
	vmwareClient, err := r.getClient()
	if err != nil {
		return err
	}
	vm, err := r.getVM()
	if err != nil {
		return err
	}
	return vmwareClient.StartVM(vm.Reference().Value)
}

func (r *VmwareProvider) StopVM(cr *v2vv1alpha1.VirtualMachineImport, client client.Client) error {
	vmwareClient, err := r.getClient()
	if err != nil {
		return err
	}
	vm, err := r.getVM()
	if err != nil {
		return err
	}
	err = vmwareClient.StopVM(vm.Reference().Value)
	if err != nil {
		return err
	}
	err = utils.AddFinalizer(cr, utils.RestoreVMStateFinalizer, client)
	if err != nil {
		return err
	}
	return nil
}

func (r *VmwareProvider) CleanUp(failure bool, cr *v2vv1alpha1.VirtualMachineImport, client client.Client) error {
	var errs []error

	err := utils.RemoveFinalizer(cr, utils.RestoreVMStateFinalizer, client)
	if err != nil {
		errs = append(errs, err)
	}

	vmiName := k8stypes.NamespacedName{
		Name:      r.vmiObjectMeta.Name,
		Namespace: r.vmiObjectMeta.Namespace,
	}

	err = r.secretsManager.DeleteFor(vmiName)
	if err != nil {
		errs = append(errs, err)
	}

	if failure {
		err = r.dataVolumesManager.DeleteFor(vmiName)
		if err != nil {
			errs = append(errs, err)
		}

		err = r.virtualMachineManager.DeleteFor(vmiName)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return foldErrors(errs, vmiName)
	}
	return nil
}

func (r *VmwareProvider) prepareDataVolumeCredentials() (*mapper.DataVolumeCredentials, error) {
	username := r.vmwareSecretDataMap[keyAccessKey]
	password := r.vmwareSecretDataMap[keySecretKey]

	secret, err := r.ensureSecretIsPresent(keyAccessKey, keySecretKey)
	if err != nil {
		return &mapper.DataVolumeCredentials{}, err
	}

	return &mapper.DataVolumeCredentials{
		URL:        r.vmwareSecretDataMap[apiUrlKey],
		Thumbprint: r.vmwareSecretDataMap[thumbprintKey],
		Username:   username,
		Password:   password,
		SecretName: secret.Name,
	}, nil
}

func (r *VmwareProvider) ensureSecretIsPresent(keyAccess, keySecret string) (*corev1.Secret, error) {
	vmiName := k8stypes.NamespacedName{
		Name:      r.vmiObjectMeta.Name,
		Namespace: r.vmiObjectMeta.Namespace,
	}
	secret, err := r.secretsManager.FindFor(vmiName)
	if err != nil {
		return nil, err
	}
	if secret == nil {
		secret, err = r.createSecret(keyAccess, keySecret)
		if err != nil {
			return nil, err
		}
	}
	return secret, nil
}

func (r *VmwareProvider) createSecret(keyAccess, keySecret string) (*corev1.Secret, error) {
	vmiName := k8stypes.NamespacedName{
		Name:      r.vmiObjectMeta.Name,
		Namespace: r.vmiObjectMeta.Namespace,
	}
	newSecret := corev1.Secret{
		Data: map[string][]byte{
			keyAccessKey:   []byte(keyAccess),
			keySecretKey:   []byte(keySecret),
		},
	}
	newSecret.OwnerReferences = []metav1.OwnerReference{
		ownerreferences.NewVMImportOwnerReference(r.vmiTypeMeta, r.vmiObjectMeta),
	}
	err := r.secretsManager.CreateFor(&newSecret, vmiName)
	if err != nil {
		return nil, err
	}
	return &newSecret, nil
}

func (r *VmwareProvider) Close() {
	// nothing to do
}

func (r *VmwareProvider) Validate() ([]v2vv1alpha1.VirtualMachineImportCondition, error) {
	// TODO: implement vmware rule validation
	return nil, nil
}

func foldErrors(errs []error, vmiName k8stypes.NamespacedName) error {
	message := ""
	for _, e := range errs {
		message = utils.WithMessage(message, e.Error())
	}
	return fmt.Errorf("clean-up for %v failed: %s", utils.ToLoggableResourceName(vmiName.Name, &vmiName.Namespace), message)
}

func updateLabels(vm *v1.VirtualMachine, labels map[string]string) {
	utils.AppendMap(vm.ObjectMeta.GetLabels(), labels)
	utils.AppendMap(vm.Spec.Template.ObjectMeta.GetLabels(), labels)
}

func updateAnnotations(vm *v1.VirtualMachine, annotationMap map[string]string) {
	annotations := vm.ObjectMeta.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
		vm.ObjectMeta.SetAnnotations(annotations)
	}
	utils.AppendMap(annotations, annotationMap)
}
