package virtualmachineimport

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/konveyor/controller/pkg/itinerary"
	"github.com/kubevirt/vm-import-operator/pkg/apis/v2v/v1beta1"
	provider "github.com/kubevirt/vm-import-operator/pkg/providers"
	"github.com/kubevirt/vm-import-operator/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	kubevirtv1 "kubevirt.io/client-go/api/v1"
	cdiv1 "kubevirt.io/containerized-data-importer/pkg/apis/core/v1alpha1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strconv"
	"strings"
	"time"
)

// Requeue
var FastReQ = time.Millisecond * 100
var PollReQ = time.Second * 3
var NoReQ = time.Duration(0)

// Steps
const (
	Created        = "Created"
	Started        = "Started"
	Prepare        = "Prepare"
	PowerOffSource = "PowerOffSource"
	CreateVM       = "CreateVM"
	PreImport      = "PreImport"
	CreateDataVolumes = "CreateDataVolumes"
	ImportDisks    = "ImportDisks"
	ConvertGuest   = "ConvertGuest"
	RestoreInitialVMState  = "RestoreInitialVMState"
	CleanUp        = "CleanUp"
	CleanUpAfterFailure = "CleanUpAfterFailure"
	Completed      = "Completed"

	ImportFailed   = "Failed"
	CreateVMFailed = "CreateVMFailed"
	DataVolumeCreationFailed = "DataVolumeCreationFailed"
	ImportDisksFailed = "ImportDisksFailed"
	ConvertGuestFailed = "ConvertGuestFailed"
)

var ColdItinerary = itinerary.Itinerary{
	Name: "ColdImport",
	Pipeline: itinerary.Pipeline{
		{Name: Created},
		{Name: Started},
		{Name: Prepare},
		{Name: PowerOffSource},
		{Name: CreateVM},
		{Name: CreateDataVolumes},
		{Name: ImportDisks},
		{Name: ConvertGuest},
		{Name: CleanUp},
		{Name: Completed},
	},
}

var FailedItinerary = itinerary.Itinerary{
	Name: "Failed",
	Pipeline: itinerary.Pipeline{
		{Name: ImportFailed},
		{Name: RestoreInitialVMState},
		{Name: CleanUpAfterFailure},
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
			t.Requeue = PollReQ
			// set VMCreationFailed condition
			return err
		}
		err = t.createVMFromSpec(vmSpec)
		if err != nil {
			return err
		}
		t.Phase = t.next(t.Phase)
	case CreateDataVolumes:
		err := t.createDataVolumes()
		if err != nil {
			// set DataVolumeCreationFailed condition
			return err
		}
		t.Phase = t.next(t.Phase)
	case ImportDisks:
		completed, err := t.importDisks()
		if err != nil {
			// set ImportFailed condition
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
		err := t.cleanUp(false)
		if err != nil {
			return err
		}
		t.Phase = t.next(t.Phase)
	case CleanUpAfterFailure:
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

func (t *Task) createDataVolumes() error {
	vm := &kubevirtv1.VirtualMachine{}
	err := t.Client.Get(context.TODO(), types.NamespacedName{Namespace: t.Owner.Namespace, Name: t.Owner.Status.TargetVMName}, vm)
	if err != nil {
		return err
	}

	dvMap, err := t.Mapper.MapDataVolumes(&t.Owner.Status.TargetVMName)
	if err != nil {
		return err
	}

	for dvKey, dv := range dvMap {
		dataVolume := &cdiv1.DataVolume{}
		err = t.Client.Get(context.TODO(), types.NamespacedName{Namespace: t.Owner.Namespace, Name: dvKey}, dataVolume)
		if err != nil && k8serrors.IsNotFound(err) {
			valid, err := t.Provider.ValidateDiskStatus(dv.Name)
			if err != nil {
				return err
			}

			if !valid {
				// should we just retry?
				return errors.New("invalid disk state")
			}

			err = t.createDataVolume(dv, vm)
			if err != nil {
				t.fail(DataVolumeCreationFailed, []string{err.Error()})
				return err
			}
			t.Mapper.MapDisk(vm, dv)
		} else if err != nil {
			return err
		}
	}

	return t.Client.Update(context.TODO(), vm)
}


func (t *Task) createDataVolume(dataVolume cdiv1.DataVolume, vm *kubevirtv1.VirtualMachine) error {
	err := controllerutil.SetControllerReference(t.Owner, &dataVolume, t.Scheme)
	if err != nil {
		return err
	}

	err = controllerutil.SetOwnerReference(vm, &dataVolume, t.Scheme)
	if err != nil {
		return err
	}

	setTrackerLabel(dataVolume.ObjectMeta, t.Owner)

	err = t.Client.Create(context.TODO(), &dataVolume)
	if err != nil {
		message := fmt.Sprintf("Data volume %s/%s creation failed: %s", dataVolume.Namespace, dataVolume.Name, err)
		t.Log.Error(err, message)
		return errors.New(message)
	}

	return nil
}

func (t *Task) importDisks() (bool, error) {
	dvMap, err := t.Mapper.MapDataVolumes(&t.Owner.Status.TargetVMName)
	if err != nil {
		return false, err
	}

	dvsDone := make(map[string]bool)
	dvsImportProgress := make(map[string]float64)
	for dvKey, _ := range dvMap {
		dataVolume := &cdiv1.DataVolume{}
		err = t.Client.Get(context.TODO(), types.NamespacedName{Namespace: t.Owner.Namespace, Name: dvKey}, dataVolume)
		if err != nil {
			return false, err
		}

		switch dataVolume.Status.Phase {
		case cdiv1.Succeeded:
			dvsDone[dvKey] = true
		case cdiv1.Pending:
			// set pending condition
			// log pending
		case cdiv1.Failed:
			// set condition
			t.fail(ImportDisksFailed, []string{"datavolume is in Failed phase"})
		case cdiv1.ImportInProgress:
			// set processing condition
			t.checkImporterPodStatus(dvKey)
		}

		dvsImportProgress[dvKey] = getImportProgress(dataVolume)
	}

	done := isDoneImport(dvsDone, len(dvMap))
	return done, nil
}

func getImportProgress(dataVolume *cdiv1.DataVolume) float64 {
	progress := string(dataVolume.Status.Progress)
	progressFloat, err := strconv.ParseFloat(strings.TrimRight(progress, "%"), 64)
	if err != nil {
		return 0.0
	} else {
		return progressFloat
	}
}

func podFailed(pod *corev1.Pod) bool {
	return pod.Status.ContainerStatuses != nil &&
		pod.Status.ContainerStatuses[0].LastTerminationState.Terminated != nil &&
		pod.Status.ContainerStatuses[0].LastTerminationState.Terminated.ExitCode > 0
}

func podExceededRestartTolerance(pod *corev1.Pod) bool {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == podCrashLoopBackOff && cs.RestartCount > int32(importPodRestartTolerance) {
			return true
		}
	}
	return false
}

func (t *Task) checkImporterPodStatus(dvKey string) {
	importerPod := &corev1.Pod{}
	err := t.Client.Get(context.TODO(), types.NamespacedName{Namespace: t.Owner.Namespace, Name: importerPodNameFromDv(dvKey)}, importerPod)
	if err == nil {
		if podFailed(importerPod) {
			// emit an event
		}
		if podExceededRestartTolerance(importerPod) {
			t.fail(ImportDisksFailed, []string{"pod CrashLoopBackoff restart limit exceeded"})
		}
	} else {
		// pod not found, or other transient problem. hang tight and requeue.
	}

}

func isDoneImport(dvsDone map[string]bool, numberOfDvs int) bool {
	// Count successfully imported dvs:
	done := utils.CountImportedDataVolumes(dvsDone)
	return done == numberOfDvs
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
	// check import without template feature gate
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
