package virtualmachineimport

import (
	"github.com/konveyor/controller/pkg/itinerary"
	"github.com/konveyor/controller/pkg/logging"
	"github.com/kubevirt/vm-import-operator/pkg/apis/v2v/v1beta1"
	provider "github.com/kubevirt/vm-import-operator/pkg/providers"
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
	ImportFailed   = "Failed"
	RestoreSource  = "RestoreSource"
	CleanUp        = "CleanUp"
	Completed      = "Completed"
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
		{Name: RestoreSource},
		{Name: CleanUp},
		{Name: Completed},
	},
}

type Task struct {
	Log logging.Logger
	Client k8sclient.Client
	Owner *v1beta1.VirtualMachineImport
	Provider *provider.Provider
	Mapper *provider.Mapper
	Step string
	Requeue time.Duration
	Itinerary itinerary.Itinerary
	Errors []string
}

func (t *Task) Run() error {
	t.Log.Info("[RUN]", "step", t.Step)
	err := t.init()
	if err != nil {
		return err
	}

	switch t.Step {
	case Created:
		t.Step = t.next(t.Step)
	case Started:
		//now := meta.Now()
		//t.Owner.Status.Started = &now
		t.Step = t.next(t.Step)
	case Prepare:
		t.Step = t.next(t.Step)
	case PowerOffSource:
		err := t.powerOffSource()
		if err != nil {
			return err
		}
		t.Step = t.next(t.Step)
	case CreateVM:
		vmSpec, err := t.createVMSpec()
		if err != nil {
			return err
		}
		err = t.createVMFromSpec(vmSpec)
		if err != nil {
			return err
		}

		t.next(t.Step)

	}

	return nil
}

func (t *Task) createVMFromSpec(vmSpec *kubevirtv1.VirtualMachine) error {

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
	if err := controllerutil.SetControllerReference(t.Owner, vmSpec, r.scheme); err != nil {
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
			t.Log.Trace(err)
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
		t.Step = t.Itinerary.Pipeline[0].Name
	}
	return nil
}

func (t *Task) failed() bool {
	// return t.Owner.HasErrors() || t.Owner.Status.HasCondition(Failed)
	return false
}

func (t *Task) warm() bool {
	// return t.Owner.Spec.Warm
	return false
}