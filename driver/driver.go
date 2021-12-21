package driver

import (
	"sync"

	"github.com/docker/go-plugins-helpers/network"
	log "github.com/sirupsen/logrus"
)

const networkType = "macvlan-noipam"

type driver struct {
	sync.Mutex
}

func NewDriver() (*driver, error) {
	return &driver{}, nil
}

func (d *driver) GetCapabilities() (*network.CapabilitiesResponse, error) {
	log.Infof("Handling GetCapabilities")
	return &network.CapabilitiesResponse{Scope: "local"}, nil
}

func (d *driver) AllocateNetwork(allocateNetworkRequest *network.AllocateNetworkRequest) (*network.AllocateNetworkResponse, error) {
	log.Infof("Handling AllocateNetwork")
	return nil, nil
}

func (d *driver) FreeNetwork(freeNetworkRequest *network.FreeNetworkRequest) error {
	log.Infof("Handling FreeNetwork")
	return nil
}

func (d *driver) CreateNetwork(createNetworkRequest *network.CreateNetworkRequest) error {
	log.Infof("Handling CreateNetwork %s", createNetworkRequest)
	return nil
}

func (d *driver) DeleteNetwork(deleteNetworkRequest *network.DeleteNetworkRequest) error {
	log.Infof("Handling DeleteNetwork %s", deleteNetworkRequest)
	return nil
}

func (d *driver) CreateEndpoint(createEndpointRequest *network.CreateEndpointRequest) (*network.CreateEndpointResponse, error) {
	log.Infof("Handling CreateEndpoint")
	return nil, nil
}

func (d *driver) DeleteEndpoint(deleteEndpointRequest *network.DeleteEndpointRequest) error {
	log.Infof("Handling DeleteEndpoint")
	return nil
}

func (d *driver) EndpointInfo(infoRequest *network.InfoRequest) (*network.InfoResponse, error) {
	log.Infof("Handling EndpointInfo")
	return nil, nil
}

func (d *driver) Join(joinRequest *network.JoinRequest) (*network.JoinResponse, error) {
	log.Infof("Handling Join")
	return nil, nil
}

func (d *driver) Leave(leaveRequest *network.LeaveRequest) error {
	log.Infof("Handling Leave")
	return nil
}

func (d *driver) DiscoverNew(discoveryNotification *network.DiscoveryNotification) error {
	log.Infof("Handling DiscoverNew")
	return nil
}

func (d *driver) DiscoverDelete(discoveryNotification *network.DiscoveryNotification) error {
	log.Infof("Handling DiscoverDelete")
	return nil
}

func (d *driver) ProgramExternalConnectivity(programExternalConnectivityRequest *network.ProgramExternalConnectivityRequest) error {
	log.Infof("Handling ProgramExternalConnectivity")
	return nil
}

func (d *driver) RevokeExternalConnectivity(revokeExternalConnectivityRequest *network.RevokeExternalConnectivityRequest) error {
	log.Infof("Handling RevokeExternalConnectivity")
	return nil
}
