package virtualmachineimport

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/konveyor/controller/pkg/itinerary"
	"github.com/kubevirt/vm-import-operator/pkg/apis/v2v/v1beta1"
	provider "github.com/kubevirt/vm-import-operator/pkg/providers"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	kubevirtv1 "kubevirt.io/client-go/api/v1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"time"
)

// Requeue
var FastReQ = time.Millisecond * 100
var PollReQ = time.Second * 3
var NoReQ = time.Duration(0)

// Steps
const (
	Created        = ""
	Started        = "Started"
	Prepare        = "Prepare"
	PowerOffSource = "PowerOffSource"
	CreateVM       = "CreateVM"
	PreImport      = "PreImport"
	ImportDisks    = "ImportDisks"
	ConvertGuest   = "ConvertGuest"
	RestoreInitialVMState  = "RestoreInitialVMState"
	CleanUp        = "CleanUp"
	Completed      = "Completed"

	ImportFailed   = "Failed"
	CreateVMFailed = "CreateVMFailed"
	ImportDisksFailed = "ImportDisksFailed"
	ConvertGuestFailed = "ConvertGuestFailed"
)

var ColdItinerary = itinerary.Itinerary{
	Name: "Cold",
	Pipeline: itinerary.Pipeline{
		{Name: Created},
		{Name: Started},
		{Name: Prepare},
		{Name: PowerOffSource},
		{Name: CreateVM},
		{Name: ImportDisks},
		{Name: ConvertGuest},
		{Name: Completed},
	},
}

var WarmItinerary = itinerary.Itinerary{
	Name: "Warm",
	Pipeline: itinerary.Pipeline{
		{Name: Created},
		{Name: Started},
		{Name: Prepare},
		{Name: PreImport},
		{Name: ImportDisks},
		{Name: ConvertGuest},
		{Name: Completed},
	},
}

var FailedItinerary = itinerary.Itinerary{
	Name: "Failed",
	Pipeline: itinerary.Pipeline{
		{Name: ImportFailed},
		{Name: RestoreInitialVMState},
		{Name: CleanUp},
		{Name: Completed},
	},
}

type Task struct {
	// k8s
	Log logr.Logger
	Client k8sclient.Client
	Scheme *runtime.Scheme
	Recorder record.EventRecorder

	// prerequisites
	Owner *v1beta1.VirtualMachineImport
	Provider provider.Provider
	Mapper provider.Mapper

	// pipeline
	Phase string
	Requeue time.Duration
	Itinerary itinerary.Itinerary
	Errors []string
}

func (t *Task) Run() error {
	t.Log.Info("[RUN]", "phase", t.Phase)
	err := t.init()
	if err != nil {
		return err
	}

	switch t.Phase {
	case Created, Started:
		t.Phase = t.next(t.Phase)
	case Prepare:
		t.Phase = t.next(t.Phase)
	case PowerOffSource:
		err := t.storeInitialVMState()
		if err != nil {
			return err
		}
		err = t.powerOffSource()
		if err != nil {
			return err
		}
		t.Phase = t.next(t.Phase)
	case CreateVM:
		vmSpec, err := t.createVMSpec()
		if err != nil {
			// set VMCreationFailed condition
			return err
		}
		err = t.createVMFromSpec(vmSpec)
		if err != nil {
			return err
		}
		t.next(t.Phase)
	case ImportDisks:
		completed, err := t.importDisks()
		if err != nil {
			// set DataVolumeCreationFailed condition
			return err
		}
		if completed {
			t.Phase = t.next(t.Phase)
		} else {
			t.Requeue = PollReQ
		}
	case ConvertGuest:
		completed, err := t.convertGuest()
		if err != nil {
			// set GuestConversionFailed condition
			return err
		}
		if completed {
			t.Phase = t.next(t.Phase)
		} else {
			t.Requeue = PollReQ
		}
	case RestoreInitialVMState:
		err := t.restoreInitialVMState()
		if err != nil {
			return err
		}
		t.Phase = t.next(t.Phase)
	case CleanUp:
		err := t.cleanUp(true)
		if err != nil {
			return err
		}
		t.Phase = t.next(t.Phase)
	case Completed:
		t.Requeue = NoReQ
		t.Log.Info("[COMPLETED]")
	default:
		t.Requeue = NoReQ
		t.Phase = Completed
	}

	return nil
}

func (t *Task) cleanUp(failed bool) error {
	return t.Provider.CleanUp(failed, t.Owner, t.Client)
}

func (t *Task) storeInitialVMState() error {
	vmStatus, err := t.Provider.GetVMStatus()
	if err != nil {
		return err
	}
	vmiCopy := t.Owner.DeepCopy()
	if vmiCopy.Annotations == nil {
		vmiCopy.Annotations = make(map[string]string)
	}
	vmiCopy.Annotations[sourceVMInitialState] = string(vmStatus)

	patch := k8sclient.MergeFrom(t.Owner)
	return t.Client.Patch(context.TODO(), vmiCopy, patch)
}

func (t *Task) restoreInitialVMState() error {
	vmInitialState, found := t.Owner.Annotations[sourceVMInitialState]
	if !found {
		return fmt.Errorf("VM didn't have initial state stored in '%s' annotation", sourceVMInitialState)
	}
	if vmInitialState == string(provider.VMStatusUp) {
		return t.Provider.StartVM()
	}
	// VM was already down
	return nil
}

func (t *Task) fail(nextStep string, reasons []string) {
	t.addErrors(reasons)

	// set conditions
	t.Phase = nextStep
}

func (t *Task) addErrors(errors []string) {
	for _, e := range errors {
		t.Errors = append(t.Errors, e)
	}
}

func (t *Task) importDisks() (bool, error) {
	return false, nil
}

func (t *Task) convertGuest() (bool, error) {
	return false, nil
}

func (t *Task) createVMFromSpec(vmSpec *kubevirtv1.VirtualMachine) error {
	err := t.Client.Create(context.TODO(), vmSpec)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		t.Log.Error(err, "Creating virtual machine failed", "Namespace", vmSpec.Namespace, "Name", vmSpec.Name)
		t.fail(CreateVMFailed, []string{err.Error()})
		return err
	}

	return nil
}

func (t *Task) createVMSpec() (*kubevirtv1.VirtualMachine, error) {
	var err error
	var vmSpec *kubevirtv1.VirtualMachine

	targetVMName := t.Mapper.ResolveVMName(t.Owner.Spec.TargetVMName)
	template, err := t.Provider.FindTemplate()

	// No template was found for this VM. If the ImportWithoutTemplate feature gate is enabled,
	// then continue the import with a blank VM spec. Otherwise, fail the import.
	if err != nil {
		if t.importWithoutTemplate() {
			t.Log.Info("No matching template was found for the source VM. Using empty VM definition.")
			vmSpec = t.Mapper.CreateEmptyVM(targetVMName)
		} else {
			t.Log.Info("No matching template was found for the source VM. Failing.")
			// try to prevent this happening via the validate step in the reconciler
			// set template matching failed condition
			// jump over to the failed migration itinerary and clean up
			return nil, err
		}
	} else {
		t.Log.Info("A template was found for the source VM", "Template.Name", template.ObjectMeta.Name)
		vmSpec, err = t.Provider.ProcessTemplate(template, targetVMName, t.Owner.Namespace)
		if err != nil {
			if t.importWithoutTemplate() {
				t.Log.Info("Failed to process the VM template. Using empty VM definition.", "Error", err.Error())
				vmSpec = t.Mapper.CreateEmptyVM(targetVMName)
			} else {
				t.Log.Info("Failed to process the VM template. Failing.", "Error", err.Error())
				return nil, err
			}
		}
		// if the template generated a name and no targetVMName was set, use that.
		if len(vmSpec.ObjectMeta.Name) > 0 && targetVMName == nil {
			targetVMName = &vmSpec.ObjectMeta.Name
		}
	}

	vmSpec, err = t.Mapper.MapVM(targetVMName, vmSpec)
	if err != nil {
		t.Log.Error(err, "Mapping VM failed.")
		return nil, err
	}

	setAnnotations(t.Owner, vmSpec)
	setTrackerLabel(vmSpec.ObjectMeta, t.Owner)
	// Set VirtualMachineImport instance as the owner and controller
	err = controllerutil.SetControllerReference(t.Owner, vmSpec, t.Scheme)
	if err != nil {
		return nil, err
	}

	return vmSpec, nil
}

func (t *Task) importWithoutTemplate() bool {
	return false
}

func (t *Task) powerOffSource() error {
	return t.Provider.StopVM(t.Owner, t.Client)
}

func (t *Task) next(phase string) string {
	step, done, err := t.Itinerary.Next(phase)
	if done || err != nil {
		if err != nil {
			t.Log.Error(err, "Error while determining next phase")
		}
		return Completed
	} else {
		return step.Name
	}
}

func (t *Task) init() error {
	t.Requeue = FastReQ
	if t.failed() {
		t.Itinerary = FailedItinerary
	} else if t.warm() {
		t.Itinerary = WarmItinerary
	} else {
		t.Itinerary = ColdItinerary
	}
	if t.Owner.Status.Itinerary != t.Itinerary.Name {
		t.Phase = t.Itinerary.Pipeline[0].Name
	}
	return nil
}

func (t *Task) failed() bool {
	//return t.Owner.HasErrors() || t.Owner.Status.HasCondition(Failed)
	//return t.Owner.Status.HasCondition(Failed)
	return false
}

func (t *Task) warm() bool {
	// return t.Owner.Spec.Warm
	return false
}