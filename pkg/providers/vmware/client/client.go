package client

import (
	"github.com/kubevirt/vm-import-operator/pkg/client"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"net/url"
	"context"
)

// RichOvirtClient is responsible for retrieving VM data from oVirt API
type richVmwareClient struct {
	client *govmomi.Client
}

// NewRichVMwareClient creates new, connected rich vmware client. After it is no longer needed, call Close().
func NewRichVMWareClient(apiUrl string, insecure bool) (client.VMClient, error) {
	u, err := url.Parse(apiUrl)
	if err != nil {
		return nil, err
	}
	govmomiClient, err := govmomi.NewClient(context.TODO(), u, insecure)
	if err != nil {
		return nil, err
	}
	vmwareClient := richVmwareClient{
		client: govmomiClient,
	}
	return &vmwareClient, nil
}

func (r richVmwareClient) GetVM(id *string, _ *string, _ *string, _ *string) (interface{}, error) {
	return r.getVM(*id)
}

func (r richVmwareClient) getVM(id string) (*object.VirtualMachine, error) {
	vm, err := find.NewFinder(r.client.Client).VirtualMachine(context.Background(), id)
	if err != nil {
		return nil, err
	}
	return vm, nil
}

func (r richVmwareClient) StopVM(id string) error {
	vm, err := r.getVM(id)
	if err != nil {
		return err
	}
	task, err := vm.PowerOff(context.TODO())
	if err != nil {
		return err
	}
	return task.Wait(context.TODO())
}

func (r richVmwareClient) StartVM(id string) error {
	vm, err := r.getVM(id)
	if err != nil {
		return err
	}
	task, err := vm.PowerOn(context.TODO())
	if err != nil {
		return err
	}
	return task.Wait(context.TODO())
}

func (r richVmwareClient) Close() error {
	return nil
}
